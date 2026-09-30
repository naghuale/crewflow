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
// modules and the toolchain of Go, and the places of secrets of the person — a folder
// of keys, a file of tokens and the patterns of the environment files. The paths are
// written out whole, because what a profile builds out of them is what a person reads
// in the settings of a run, and a machine of a test is not there.
var thePolicy = access.Policy{
	Read: []access.Grant{
		{Path: "/Users/someone/go/pkg/mod", Source: access.SourceAccess, Reason: "the project asked for it with `go env GOMODCACHE`"},
		{Path: "/usr/local/go", Source: access.SourceTask, Reason: "the task reads the source of the toolchain"},
	},
	Deny: []string{
		"/Users/someone/.ssh", "/Users/someone/.netrc", "**/.env", "**/.env.*",
	},
}

// TestAccessEnvOfOpencode is the settings of a run, letter for letter: the folders the
// executor may read outside its worktree, the places that are closed to it for reading
// and for writing, a place of secrets as the place itself and as everything under it
// (a secret is a file as often as a folder), and the `.env` of the worktree itself,
// which is inside it and out of the reach of a rule about the outside. The file is the
// golden of it, because these are the bytes an agent of a real run is handed, and a
// rule that moved in them is a permission that moved.
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
// their own in the environment, and the rules of a run are written into them key by
// key, not over the whole table. A rule of a person for another pattern or another
// tool stays, a rule of a run for the same pattern wins, and a permission that is a
// single word has no keys to keep and is replaced (docs/DESIGN.md §7d).
func TestAccessEnvOfOpencodeOverTheSettingsOfThePerson(t *testing.T) {
	environ := []string{
		"HOME=/Users/someone",
		configContent + `={"$schema":"https://opencode.ai/config.json","model":"someone/model",` +
			`"permission":{"bash":"ask",` +
			`"external_directory":{"**":"ask","/Users/someone/go/pkg/mod/**":"deny"},` +
			`"read":{"**":"allow","/Users/someone/.netrc":"allow"},` +
			`"edit":{"*.md":"allow"}}}`,
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
			Read              map[string]string `json:"read"`
			Edit              map[string]string `json:"edit"`
		} `json:"permission"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], configContent+"=")), &settings); err != nil {
		t.Fatalf("the settings of the run are not JSON: %v", err)
	}
	// What is not a permission of a run is the settings of the person and stays as it
	// was: a model, a tool, and a rule for a pattern crewflow writes nothing about.
	if settings.Model != "someone/model" {
		t.Errorf("the model = %q, want the one the person had", settings.Model)
	}
	if settings.Permission.Bash != "ask" {
		t.Errorf("the permission of bash = %v, want the one the person had", settings.Permission.Bash)
	}
	kept := []struct {
		name  string
		table map[string]string
		key   string
		want  string
	}{
		{"external_directory", settings.Permission.ExternalDirectory, "**", "ask"},
		{"read", settings.Permission.Read, "**", "allow"},
		{"edit", settings.Permission.Edit, "*.md", "allow"},
	}
	for _, k := range kept {
		if got := k.table[k.key]; got != k.want {
			t.Errorf("the rule %q of the person for %q = %q, want %q: a rule of a run is written key by key",
				k.name, k.key, got, k.want)
		}
	}
	// The keys a run writes are its own, whatever the person wrote for them: the folder
	// of the dependencies is open for reading, and the file of a secret is closed even
	// where the person had allowed it — "**" under a place does not match the place.
	won := []struct {
		name  string
		table map[string]string
		key   string
		want  string
	}{
		{"external_directory", settings.Permission.ExternalDirectory, "/Users/someone/go/pkg/mod/**", "allow"},
		{"external_directory", settings.Permission.ExternalDirectory, "/Users/someone/.ssh", "deny"},
		{"external_directory", settings.Permission.ExternalDirectory, "/Users/someone/.ssh/**", "deny"},
		{"read", settings.Permission.Read, "/Users/someone/.netrc", "deny"},
		{"read", settings.Permission.Read, "**/.env", "deny"},
		{"edit", settings.Permission.Edit, "/usr/local/go/**", "deny"},
	}
	for _, w := range won {
		if got := w.table[w.key]; got != w.want {
			t.Errorf("%s[%q] = %q, want %q: the rules of a run win on a key of their own", w.name, w.key, got, w.want)
		}
	}
}

// TestAccessEnvOfAPermissionThatIsAWord: a permission that is only "allow" or "deny"
// has no keys to merge into, so the rules of a run are the whole of it. A person who
// wrote that got no table, and a run may not leave a table of rules beside a word that
// says something else.
func TestAccessEnvOfAPermissionThatIsAWord(t *testing.T) {
	env, err := (opencode{}).AccessEnv(thePolicy, []string{
		configContent + `={"permission":{"edit":"deny","read":"allow"}}`,
	})
	if err != nil {
		t.Fatalf("AccessEnv returned an error: %v", err)
	}

	var settings struct {
		Permission struct {
			Edit map[string]string `json:"edit"`
			Read map[string]string `json:"read"`
		} `json:"permission"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], configContent+"=")), &settings); err != nil {
		t.Fatalf("the settings of the run are not JSON: %v", err)
	}
	if got := settings.Permission.Edit["/Users/someone/.ssh"]; got != "deny" {
		t.Errorf("the rules of writing = %+v, want the rules of the run in them", settings.Permission.Edit)
	}
	if got := settings.Permission.Read["**/.env"]; got != "deny" {
		t.Errorf("the rules of reading = %+v, want the rules of the run in them", settings.Permission.Read)
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
		denyVar + "=/Users/someone/.ssh:/Users/someone/.netrc:**/.env:**/.env.*",
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
