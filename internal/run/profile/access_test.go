package profile

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/access"
)

// thePolicy is the reading policy of a run of this repository: the cache of the
// modules and the toolchain of Go, and the places of secrets of the person. The
// paths are written out whole, because what a profile builds out of them is what a
// person reads in the settings of a run, and a machine of a test is not there.
var thePolicy = access.Policy{
	Read: []string{"/Users/someone/go/pkg/mod", "/usr/local/go"},
	Deny: []string{"/Users/someone/.ssh", "/Users/someone/.aws", "**/.env", "**/.env.*"},
}

// TestAccessEnvOfOpencode is the settings of a run, letter for letter: what the
// executor of a project may read outside its worktree, what it may never read, and
// the same folders for writing, which a run never allows. The file is the golden of
// it, because these are the bytes an agent of a real run is handed, and a rule that
// moved in them is a permission that moved.
func TestAccessEnvOfOpencode(t *testing.T) {
	got, err := (opencode{}).AccessEnv(thePolicy, nil)
	if err != nil {
		t.Fatalf("AccessEnv returned an error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("AccessEnv = %q, want the one variable of the settings of OpenCode", got)
	}
	name, content, found := strings.Cut(got[0], "=")
	if !found || name != configContent {
		t.Fatalf("AccessEnv = %q, want it to set %s", got[0], configContent)
	}
	want := string(golden(t))
	if content != want {
		t.Errorf("the settings of the run =\n%s\nwant\n%s", pretty(t, content), pretty(t, want))
	}
}

// TestAccessEnvOfOpencodeOverTheSettingsOfThePerson: a person may have settings of
// their own in the environment, and the rules of the run are written over them: what
// a run may read is what the policy of the run says. What is not a permission is
// left alone, because the settings of a person are not crewflow's to throw away.
func TestAccessEnvOfOpencodeOverTheSettingsOfThePerson(t *testing.T) {
	environ := []string{
		"HOME=/Users/someone",
		configContent + `={"$schema":"https://opencode.ai/config.json","model":"someone/model",` +
			`"permission":{"bash":"ask","external_directory":{"**":"ask"}}}`,
	}

	env, err := (opencode{}).AccessEnv(thePolicy, environ)
	if err != nil {
		t.Fatalf("AccessEnv returned an error: %v", err)
	}

	var settings struct {
		Model      string `json:"model"`
		Permission struct {
			Bash              any               `json:"bash"`
			ExternalDirectory map[string]string `json:"external_directory"`
			Edit              map[string]string `json:"edit"`
		} `json:"permission"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], configContent+"=")), &settings); err != nil {
		t.Fatalf("the settings of the run are not JSON: %v", err)
	}
	if settings.Model != "someone/model" {
		t.Errorf("the model = %q, want the one the person had, want the settings of a person kept", settings.Model)
	}
	if settings.Permission.Bash != "ask" {
		t.Errorf("the permission of bash = %v, want the one the person had", settings.Permission.Bash)
	}
	// The rule of a person over every path is replaced by the rules of the run, and a
	// place that is closed in both lists stays closed.
	if _, asked := settings.Permission.ExternalDirectory["**"]; asked {
		t.Error("the rule of the person over every path is still there, want the rules of the run")
	}
	for path, want := range map[string]string{
		"/Users/someone/go/pkg/mod/**": "allow",
		"/usr/local/go/**":             "allow",
		"/Users/someone/.ssh/**":       "deny",
		"**/.env":                      "deny",
	} {
		if got := settings.Permission.ExternalDirectory[path]; got != want {
			t.Errorf("the rule for %q is %q, want %q", path, got, want)
		}
		if got := settings.Permission.Edit[path]; got != "deny" {
			t.Errorf("the rule for writing %q is %q, want it closed for a folder that may be read", path, got)
		}
	}
}

// TestAccessEnvOfAValueThatIsNotJSON: a run that cannot say what it may read does not
// start. The agent would go by the settings of the person and crewflow would believe
// it had said otherwise, and a run that is refused a permission for a folder it was
// given stops without doing anything.
func TestAccessEnvOfAValueThatIsNotJSON(t *testing.T) {
	cases := []struct {
		name   string
		given  string
		wanted string
	}{
		{name: "a value that is not JSON at all", given: "permission=allow", wanted: configContent},
		{name: "a JSON that is cut in half", given: `{"permission":`, wanted: configContent},
		{name: "JSON that is not a settings", given: "[]", wanted: configContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := (opencode{}).AccessEnv(thePolicy, []string{configContent + "=" + tc.given})
			if err == nil {
				t.Fatalf("AccessEnv = %q, want an error before the run is started", env)
			}
			if !strings.Contains(err.Error(), tc.wanted) {
				t.Errorf("error %q does not name %q", err, tc.wanted)
			}
		})
	}
}

// TestAccessEnvOfNullIsTheSettingsOfNobody: a settings of nothing is a settings of
// nobody, and the rules of the run are written into it all the same.
func TestAccessEnvOfNullIsTheSettingsOfNobody(t *testing.T) {
	env, err := (opencode{}).AccessEnv(thePolicy, []string{configContent + "=null"})
	if err != nil {
		t.Fatalf("AccessEnv returned an error: %v", err)
	}
	if !strings.Contains(env[0], `"external_directory"`) {
		t.Errorf("the settings of the run = %q, want the rules of the policy in them", env[0])
	}
}

// TestAccessEnvOfAGenericAgent: an agent crewflow has not run is told the policy in
// the two lists of the environment, whole, and nothing else: crewflow does not guess
// how an agent takes a permission, and a wrapper of the agent is what holds it to
// them (docs/DESIGN.md §7b).
func TestAccessEnvOfAGenericAgent(t *testing.T) {
	env, err := (generic{}).AccessEnv(thePolicy, nil)
	if err != nil {
		t.Fatalf("AccessEnv returned an error: %v", err)
	}

	want := []string{
		readVar + "=/Users/someone/go/pkg/mod:/usr/local/go",
		denyVar + "=/Users/someone/.ssh:/Users/someone/.aws:**/.env:**/.env.*",
	}
	if !slices.Equal(env, want) {
		t.Errorf("AccessEnv = %q, want %q", env, want)
	}
}

// TestAccessEnvOfAnEmptyPolicy: a project that named nothing has an executor that may
// read its worktree and nothing else, and the settings of a run say so rather than
// naming a folder nobody allowed.
func TestAccessEnvOfAnEmptyPolicy(t *testing.T) {
	env, err := (opencode{}).AccessEnv(access.Policy{}, nil)
	if err != nil {
		t.Fatalf("AccessEnv returned an error: %v", err)
	}
	if !strings.Contains(env[0], `"external_directory":{}`) {
		t.Errorf("the settings of the run = %q, want no folder opened", env[0])
	}
}

// TestEveryProfileNamesTheRights: a run hands the policy to the agent through its
// profile, and a profile that cannot is a run that would stop at the first folder of
// a dependency.
func TestEveryProfileNamesTheRights(t *testing.T) {
	for _, profile := range []Profile{opencode{}, generic{}} {
		env, err := profile.AccessEnv(thePolicy, nil)
		if err != nil {
			t.Errorf("the profile %q returned an error: %v", profile.Name(), err)
			continue
		}
		if len(env) == 0 || strings.TrimSpace(strings.Join(env, "")) == "" {
			t.Errorf("the profile %q handed the executor no rights at all", profile.Name())
		}
		for _, variable := range env {
			name, value, found := strings.Cut(variable, "=")
			if !found || name == "" || value == "" {
				t.Errorf("the profile %q handed the executor %q, want NAME=value", profile.Name(), variable)
			}
		}
	}
}

// golden is the settings of a run as they were written down when the rules of it were
// made: the file is the answer, and a test that changes the rights of a run changes it
// on purpose.
func golden(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/access.json")
	if err != nil {
		t.Fatalf("read the golden of the settings of a run: %v", err)
	}
	return []byte(strings.TrimRight(string(data), "\n"))
}

// pretty is a value of a variable as a person reads it: one line of the environment
// is one line of JSON here too, and it is read, not measured.
func pretty(t *testing.T, value string) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(value), "", "  "); err != nil {
		t.Fatalf("the value is not JSON: %v", err)
	}
	return out.String()
}
