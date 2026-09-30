package access

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
)

// TestResolve walks the paths a project may name, one case per rule of the policy: a
// folder of a library is opened, and a place that holds a secret is not, whatever the
// project wrote in its file. The home of the person is a folder of the test, so that
// no case here reads a secret of the machine it runs on.
func TestResolve(t *testing.T) {
	home := real(t.TempDir())
	library := folder(t, filepath.Join(home, "go", "pkg", "mod"))
	sdk := folder(t, filepath.Join(home, "sdk"))
	keys := folder(t, filepath.Join(home, ".ssh"))
	// The two links are the ways a project names a place of secrets without saying
	// so: one that leads there and one that leads to the whole of the home.
	link := filepath.Join(home, "lib")
	if err := os.Symlink(keys, link); err != nil {
		t.Fatalf("make the link: %v", err)
	}
	linkHome := filepath.Join(home, "keys")
	if err := os.Symlink(home, linkHome); err != nil {
		t.Fatalf("make the link: %v", err)
	}
	cases := []struct {
		name string
		// readFrom is what the project asks, and read what it names outright.
		readFrom [][]string
		read     []string
		// want is what the executor may read, and why is what a person is told about
		// the paths that were refused.
		want []string
		why  string
	}{
		{
			name:     "the folder the cache of the modules is in",
			readFrom: [][]string{{"go", "env", "GOMODCACHE"}},
			want:     []string{library},
		},
		{
			name: "a folder of a library named in the file",
			read: []string{"~/go/pkg/mod", sdk},
			want: []string{library, sdk},
		},
		{
			name: "the same folder twice",
			// The tool named it, the file named it, and the executor is told once.
			readFrom: [][]string{{"go", "env", "GOMODCACHE"}},
			read:     []string{"~/go/pkg/mod", library},
			want:     []string{library},
		},
		{
			name: "the root of a disk",
			read: []string{"/"},
			why:  "the root of a disk",
		},
		{
			name: "the home of the person",
			read: []string{"~"},
			why:  "the home of the person",
		},
		{
			name: "a folder that holds a place of secrets",
			read: []string{filepath.Join(home, "Library")},
			why:  "which is where secrets are",
		},
		{
			name: "a place of secrets itself",
			read: []string{keys},
			why:  "which is where secrets are",
		},
		{
			name: "a link to a place of secrets",
			read: []string{link},
			why:  "which is where secrets are",
		},
		{
			name: "a link to the home of the person",
			read: []string{linkHome},
			why:  "the home of the person",
		},
		{
			name: "a path that is not there",
			read: []string{filepath.Join(home, "go", "src"), "  "},
		},
		{
			name: "a path that is not absolute",
			read: []string{"go/pkg/mod"},
			why:  "not an absolute path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The tool of the project prints the folder of the cache and an empty
			// line among them: both are the way `go env` answers.
			m := machine{prints: map[string]string{
				"go env GOMODCACHE": library + "\n\n",
			}}

			policy, problems := Resolve(t.Context(), Env{Home: home, Run: m.run, Timeout: time.Second},
				config.Access{ReadFrom: tc.readFrom, Read: tc.read}, nil)

			if !slices.Equal(Paths(policy.Read), tc.want) {
				t.Errorf("the policy allows reading %v, want %v", Paths(policy.Read), tc.want)
			}
			switch {
			case tc.why == "" && len(problems) != 0:
				t.Errorf("the problems = %+v, want none", problems)
			case tc.why != "":
				if len(problems) != 1 || !strings.Contains(problems[0].Reason, tc.why) {
					t.Errorf("the problems = %+v, want one that says %q", problems, tc.why)
				} else if problems[0].Path == "" {
					t.Error("a problem names no path, want the path the project wrote")
				}
			}
		})
	}
}

// TestResolveNamesTheHandBehindEveryFolder is the reason every folder of the policy
// comes out with a source and a reason: the rights of the agent are written from the
// paths, and a report and an assignment of a run are written from all of it. A path
// alone does not say whether the project needs the folder on every machine of it or
// one task of it needs it once, and a person deciding about a run cannot tell those
// two apart (docs/DESIGN.md §7d, §7f).
func TestResolveNamesTheHandBehindEveryFolder(t *testing.T) {
	home := real(t.TempDir())
	library := folder(t, filepath.Join(home, "go", "pkg", "mod"))
	toolchain := folder(t, filepath.Join(home, "sdk"))
	other := folder(t, filepath.Join(home, "cache"))
	m := machine{prints: map[string]string{"go env GOMODCACHE": library + "\n"}}

	policy, problems := Resolve(t.Context(), Env{Home: home, Run: m.run},
		config.Access{
			ReadFrom: [][]string{{"go", "env", "GOMODCACHE"}},
			Read:     []string{filepath.Join(home, "sdk")},
		}, []Asked{
			{Path: filepath.Join(home, "cache"), Reason: "the task checks the cache of the toolchain"},
		})

	if len(problems) != 0 {
		t.Fatalf("the problems = %+v, want none", problems)
	}
	want := []Grant{
		{Path: toolchain, Source: SourceAccess, Reason: namedInFile},
		{Path: library, Source: SourceAccess, Reason: "the project asked for it with `go env GOMODCACHE`"},
		{Path: other, Source: SourceTask, Reason: "the task checks the cache of the toolchain"},
	}
	if !slices.Equal(policy.Read, want) {
		t.Errorf("the policy allows reading %+v, want %+v", policy.Read, want)
	}
}

// TestResolveAnswersTheProjectBeforeTheTask: a folder both the file of the project and
// a task named is opened once and the project is the one behind it. The project is
// what every run of it is held to and the task is what this one is, and a run that
// showed a folder of the project as a folder of a task would make every run of it look
// like a one-off grant (docs/DESIGN.md §7d, §7f).
func TestResolveAnswersTheProjectBeforeTheTask(t *testing.T) {
	home := real(t.TempDir())
	library := folder(t, filepath.Join(home, "go", "pkg", "mod"))

	policy, problems := Resolve(t.Context(), Env{Home: home, Run: machine{}.run},
		config.Access{Read: []string{library}},
		[]Asked{{Path: library, Reason: "this task happened to need the same folder"}})

	if len(problems) != 0 {
		t.Fatalf("the problems = %+v, want none", problems)
	}
	if len(policy.Read) != 1 || policy.Read[0].Source != SourceAccess {
		t.Errorf("the policy allows reading %+v, want the folder once and named as the project", policy.Read)
	}
}

// TestResolveRefusesThePathOfATaskInAPlaceOfSecrets: the ban of a place of secrets is
// stronger than the ask of the file of the project and stronger than the ask of the
// task, and the problem says which hand asked for it — a caller has to tell a folder
// the project could not have from a folder the task cannot do without, and the second
// one stops the run before it starts (§7d, §7f).
func TestResolveRefusesThePathOfATaskInAPlaceOfSecrets(t *testing.T) {
	home := real(t.TempDir())
	keys := folder(t, filepath.Join(home, ".ssh"))

	policy, problems := Resolve(t.Context(), Env{Home: home, Run: machine{}.run}, config.Access{}, []Asked{
		{Path: keys, Reason: "the task wants to look at the keys"},
	})

	if len(policy.Read) != 0 {
		t.Errorf("the policy allows reading %v, want nothing: the place of secrets is closed", Paths(policy.Read))
	}
	if len(problems) != 1 {
		t.Fatalf("the problems = %+v, want one", problems)
	}
	if problems[0].Source != SourceTask {
		t.Errorf("the problem names the source %q, want %q", problems[0].Source, SourceTask)
	}
	if !strings.Contains(problems[0].Reason, "which is where secrets are") {
		t.Errorf("the reason = %q, want it to say that the place holds secrets", problems[0].Reason)
	}
}

// TestResolveClosesThePlacesOfSecrets: what is closed does not depend on the file of
// the project. The keys of the person are closed to the executor of every project on
// this machine, and the patterns of `.env` are the same on every one of them.
func TestResolveClosesThePlacesOfSecrets(t *testing.T) {
	home := real(t.TempDir())
	folder(t, filepath.Join(home, ".ssh"))

	policy, problems := Resolve(t.Context(), Env{Home: home, Run: machine{}.run}, config.Access{}, nil)

	// The list is written out here and not taken from the package: a secret that is
	// added to the package and not to this list is a secret crewflow has and a test
	// does not know about. The home of the test is a folder this machine holds, so
	// every place of it is in one spelling and the list is the list of the secrets.
	want := []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, "Library", "Keychains"),
		filepath.Join(home, ".config", "gh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".docker", "config.json"),
		filepath.Join(home, ".kube"),
		"**/.env",
		"**/.env.*",
	}
	if !slices.Equal(policy.Deny, want) {
		t.Errorf("the places that are closed = %v, want %v", policy.Deny, want)
	}
	if len(policy.Read) != 0 || len(problems) != 0 {
		t.Errorf("a project that named nothing got %v and %+v, want no folder to read and no problems",
			Paths(policy.Read), problems)
	}
}

// TestResolveNamesEveryPlaceInBothSpellings is the case the macOS runner of a run of
// the pilot found: `/var` and `/tmp` are links into `/private`, and an agent asks about
// a path in the spelling it wrote, so a rule in the spelling of the machine does not
// cover the one the agent uses. Every place is named twice — as the project wrote it
// and with every link above it followed — for the folders that may be read and for the
// places that are closed, whether they are there or not.
//
// The link is made here, in a folder of the test, so that the case is the same on a
// machine that has no links in `/var` and on one that has.
func TestResolveNamesEveryPlaceInBothSpellings(t *testing.T) {
	// The home of the person is a link to a folder of the test, which is what `/var` is
	// on macOS: two names, one folder. The folder is taken as this machine holds it, so
	// that the two spellings the test is about are the two of the link and not three.
	held := folder(t, filepath.Join(real(t.TempDir()), "home"))
	home := filepath.Join(held, "..", "link")
	if err := os.Symlink(held, home); err != nil {
		t.Fatalf("make the link of the home: %v", err)
	}
	// The folder of the dependencies is named the way the agent would name it, through
	// the link, and one of the places of secrets is named the other way, through the
	// folder the link leads to: the two spellings have to be in the policy both times.
	library := folder(t, filepath.Join(held, "go", "pkg", "mod"))
	keys := folder(t, filepath.Join(held, ".ssh"))
	m := machine{prints: map[string]string{
		"go env GOMODCACHE": filepath.Join(home, "go", "pkg", "mod") + "\n",
	}}

	policy, problems := Resolve(t.Context(), Env{Home: home, Run: m.run, Timeout: time.Second},
		config.Access{ReadFrom: [][]string{{"go", "env", "GOMODCACHE"}}}, nil)

	if len(problems) != 0 {
		t.Errorf("the problems = %+v, want none: a folder under a link is still a folder", problems)
	}
	want := []string{filepath.Join(home, "go", "pkg", "mod"), library}
	if !slices.Equal(Paths(policy.Read), want) {
		t.Errorf("the policy allows reading %v, want the folder in both spellings %v", Paths(policy.Read), want)
	}
	// The place of secrets that is not there is closed in both spellings all the same:
	// nothing is read because a rule is in another spelling than the agent's, and a
	// folder of a person that does not exist yet is still the one its home holds.
	for _, want := range []string{filepath.Join(home, ".ssh"), keys, filepath.Join(home, ".netrc"), filepath.Join(held, ".netrc"), "**/.env"} {
		if !slices.Contains(policy.Deny, want) {
			t.Errorf("the closed places are %v, want %q among them", policy.Deny, want)
		}
	}
	// The home of the person is one folder in two names, and neither of them opens.
	for _, spelling := range []string{home, held} {
		refused, reason := Resolve(t.Context(), Env{Home: home, Run: m.run}, config.Access{Read: []string{spelling}}, nil)
		if len(refused.Read) != 0 || len(reason) == 0 {
			t.Errorf("the home of the person named as %q was opened (%v, %+v), want it refused", spelling, Paths(refused.Read), reason)
		}
	}
}

// TestResolveAsksTheCommands walks what a tool of the project may answer: the paths it
// printed one per line, the empty lines among them left out, and the commands that
// failed or hung coming back as a problem with the words of the tool in it. A tool
// that never answers is not a task that stands still: a run has an end.
func TestResolveAsksTheCommands(t *testing.T) {
	home := real(t.TempDir())
	library := folder(t, filepath.Join(home, "go"))
	cases := []struct {
		name string
		// answer is what the command of the project does.
		answer answer
		want   []string
		why    string
	}{
		{
			name:   "a tool that names the folder of its cache",
			answer: answer{stdout: library + "\n"},
			want:   []string{library},
		},
		{
			name:   "a tool that names nothing",
			answer: answer{stdout: "\n\n"},
		},
		{
			name:   "a tool that failed",
			answer: answer{stderr: "go: cannot find GOMODCACHE\n", code: 1},
			why:    "exited with 1: go: cannot find GOMODCACHE",
		},
		{
			name:   "a tool that could not be started",
			answer: answer{err: fmt.Errorf("exec: %q: not found in $PATH", "go")},
			why:    "did not run",
		},
		{
			name:   "a tool that never answers",
			answer: answer{hangs: true},
			why:    "did not run",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := machine{prints: map[string]string{}, answers: map[string]answer{
				"go env GOMODCACHE": tc.answer,
			}}

			policy, problems := Resolve(t.Context(), Env{Home: home, Run: m.run, Timeout: 20 * time.Millisecond},
				config.Access{ReadFrom: [][]string{{"go", "env", "GOMODCACHE"}}}, nil)

			if !slices.Equal(Paths(policy.Read), tc.want) {
				t.Errorf("the policy allows reading %v, want %v", Paths(policy.Read), tc.want)
			}
			switch {
			case tc.why == "" && len(problems) != 0:
				t.Errorf("the problems = %+v, want none", problems)
			case tc.why != "":
				if len(problems) != 1 {
					t.Fatalf("the problems = %+v, want one that says %q", problems, tc.why)
				}
				if !strings.Contains(problems[0].Reason, tc.why) {
					t.Errorf("the reason = %q, want it to say %q", problems[0].Reason, tc.why)
				}
				if problems[0].Path != "go env GOMODCACHE" {
					t.Errorf("the problem names %q, want the command a person may run by hand", problems[0].Path)
				}
			}
		})
	}
}

// TestResolveAsksEveryCommandTheProjectNamed: the commands are the project saying
// where its dependencies are, and one of them that says nothing does not stop the
// others from being asked.
func TestResolveAsksEveryCommandTheProjectNamed(t *testing.T) {
	home := real(t.TempDir())
	first := folder(t, filepath.Join(home, "go", "pkg", "mod"))
	second := folder(t, filepath.Join(home, "sdk"))
	m := machine{prints: map[string]string{
		"go env GOMODCACHE":     first + "\n",
		"xcrun --show-sdk-path": second + "\n",
	}}

	policy, problems := Resolve(t.Context(), Env{Home: home, Run: m.run},
		config.Access{ReadFrom: [][]string{
			{"go", "env", "GOMODCACHE"},
			{"xcrun", "--show-sdk-path"},
		}}, nil)

	if want := []string{first, second}; !slices.Equal(Paths(policy.Read), want) {
		t.Errorf("the policy allows reading %v, want %v", Paths(policy.Read), want)
	}
	if len(problems) != 0 {
		t.Errorf("the problems = %+v, want none", problems)
	}
}

// TestResolveOfAMachineWithoutAHome: "~" stands for the home of the person, and a
// machine that cannot say where it is has no folder to resolve it to. The paths that
// stand for it are refused with that said out loud, not read as they are.
func TestResolveOfAMachineWithoutAHome(t *testing.T) {
	policy, problems := Resolve(t.Context(), Env{Run: machine{}.run}, config.Access{Read: []string{"~/go"}}, nil)

	if len(policy.Read) != 0 {
		t.Errorf("the policy allows reading %v, want nothing", Paths(policy.Read))
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "it has none") {
		t.Errorf("the problems = %+v, want one that says that the machine has no home", problems)
	}
}

// TestDenyListIsNotTheListOfThePackage: a caller holds on to the list it is given,
// and the next run must not find its own paths in it.
func TestDenyListIsNotTheListOfThePackage(t *testing.T) {
	first := denyList(real(t.TempDir()))
	first[0] = "/somewhere/else"

	if second := denyList(real(t.TempDir())); slices.Equal(first, second) {
		t.Error("two policies of two runs hold one list, want a list of their own")
	}
}

// machine is the machine a policy is worked out on: what every command of the project
// answers, with or without an answer. It stands in for the tools of a project, so
// that no test of the policy runs a tool of the machine it runs on.
type machine struct {
	// prints is what a command line answers with, and answers the whole of a command
	// line for a case that is about what the tool said.
	prints  map[string]string
	answers map[string]answer
}

// answer is what a command of the project does when crewflow asks it.
type answer struct {
	stdout string
	stderr string
	code   int
	err    error
	// hangs says that the tool waits for someone who is not there.
	hangs bool
}

// run is the machine answering a command of the project. A tool that hangs is left to
// be stopped by the time limit of the command, as a real one is.
func (m machine) run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch {
	case m.answers[line].hangs:
		<-ctx.Done()
		return nil, nil, 0, ctx.Err()
	case m.answers != nil:
		if a, ok := m.answers[line]; ok {
			return []byte(a.stdout), []byte(a.stderr), a.code, a.err
		}
	}
	if printed, ok := m.prints[line]; ok {
		return []byte(printed), nil, 0, nil
	}
	return nil, nil, 0, fmt.Errorf("exec: %q: not found in $PATH", name)
}

// folder is a folder of the test, made and named: a case of the policy needs
// somewhere on the machine to be true of, and every path of a test is the one this
// machine holds it under, links and all.
func folder(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("make %s: %v", path, err)
	}
	return real(path)
}
