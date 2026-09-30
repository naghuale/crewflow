package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// tasksOfTheRegistry are the issues the registry is allowed to name: the tasks of
// the milestone that write what the design describes and crewflow has not. A
// registry that names anything else names a task nobody waits for, and the
// registry is where a person reads what to wait for.
var tasksOfTheRegistry = []int{51, 54, 55, 56, 57, 58, 59, 60, 61, 62}

// choices are the keys of the file whose values crewflow names, with the values
// Validate allows: every one of them is in the registry, either as a value
// crewflow applies or as one it refuses.
var choices = map[string][]string{
	"forge.kind":           forgeKinds,
	"tracker.kind":         trackerKinds,
	"ci.kind":              ciKinds,
	"identity.mode":        identityModes,
	"isolation.mode":       isolationModes,
	"merge.by":             mergeBys,
	"merge.strategy":       mergeStrategies,
	"merge.via":            mergeVias,
	"tasks.owner_approval": ownerApprovals,
}

// TestLoadRefusesEveryValueTheRegistryNames walks the values of §"Доказательство
// тестами" of the task: a value that turns on a behaviour the design describes
// and the code has not written is an error of the load that names the key, the
// task that writes it and what to write instead (docs/DESIGN.md §5).
func TestLoadRefusesEveryValueTheRegistryNames(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		want  []string
	}{
		{
			name:  "isolation.mode = sandbox",
			extra: "\n[isolation]\nmode = \"sandbox\"\n",
			want:  []string{"isolation.mode", "sandbox", "crewflow#55", `mode = "host"`},
		},
		{
			name:  "merge.via = forge",
			extra: "\n[merge]\nvia = \"forge\"\n",
			want:  []string{"merge.via", "forge", "crewflow#58", `via = "git-push"`},
		},
		{
			name:  "parallel.max_tasks = 2",
			extra: "\n[parallel]\nmax_tasks = 2\n",
			want:  []string{"parallel.max_tasks", "crewflow#56", "max_tasks = 1"},
		},
		{
			name:  "a declared capability",
			extra: "\n[[capabilities]]\nname = \"github-keys\"\nuse = [\"gh\"]\n",
			want:  []string{"capabilities", "crewflow#54"},
		},
		{
			name:  "a fallback executor",
			extra: "\n[[executor.fallback]]\ncommand = [\"other-agent\", \"run\", \"{prompt}\"]\n",
			want:  []string{"executor.fallback", "crewflow#51", "fallback = []"},
		},
		{
			name:  "forge.kind = gitlab",
			extra: "\n[forge]\nkind = \"gitlab\"\n",
			want:  []string{"forge.kind", "gitlab", "crewflow#60", `kind = "github"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crewflow.toml")
			writeFile(t, path, baseConfig+tc.extra)

			_, err := Load(path)

			if err == nil {
				t.Fatalf("Load of a file with %s returned no error, want the refusal of the registry", tc.name)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			var refusal *NotImplemented
			if !errors.As(err, &refusal) {
				t.Fatalf("error %v is not a refusal of the registry, want a *NotImplemented a report can show", err)
			}
			if len(refusal.Settings) == 0 {
				t.Error("the refusal holds no setting, want the key that asked")
			}
		})
	}
}

// TestLoadAcceptsTheDefaultsOfEveryKey is the other side of the same rule: the
// defaults of the file are what crewflow does, so a project that says nothing
// loads and a task may start (docs/DESIGN.md §5).
func TestLoadAcceptsTheDefaultsOfEveryKey(t *testing.T) {
	if _, err := loadFile(t, "minimal.toml"); err != nil {
		t.Fatalf("Load(minimal.toml) returned an error: %v", err)
	}
	for _, asked := range (Config{}).asked() {
		t.Errorf("the defaults of the file ask for %s, want them to load", asked)
	}
}

// TestTheFileOfThisRepositoryLoads is the file of the project that writes
// crewflow: a key of it that asks for what crewflow does not do would make the
// file of this repository unloadable, and every command of the repository would
// refuse to start (docs/DESIGN.md §5).
func TestTheFileOfThisRepositoryLoads(t *testing.T) {
	path := filepath.Join(rootOfTheModule(t), "crewflow.toml")

	if _, err := Load(path); err != nil {
		t.Errorf("Load(%s) returned an error: %v", path, err)
	}
}

// TestMT004EveryFieldOfTheFileIsInTheRegistry is the first rule of the registry:
// every key of the file is in it. A key that is in the file and in no registry is
// a key nobody has said whether crewflow applies it, and the day it promises
// something crewflow does not do, nothing says so (docs/DESIGN.md §5).
func TestMT004EveryFieldOfTheFileIsInTheRegistry(t *testing.T) {
	for _, field := range fieldsMissingFrom(keysOfTheFile(t), settings) {
		t.Errorf("the field %q of the file is in no registry: what crewflow does with it is unknown", field)
	}
}

// TestMT004FailsWhenAFieldIsNotInTheRegistry shows the rule and not only that it
// holds today: a field left out of the registry is the mistake it is there for,
// and the rule has to be shown failing.
func TestMT004FailsWhenAFieldIsNotInTheRegistry(t *testing.T) {
	withoutIsolation := slices.DeleteFunc(slices.Clone(settings), func(setting Setting) bool {
		return setting.Key == "isolation.mode"
	})

	missing := fieldsMissingFrom(keysOfTheFile(t), withoutIsolation)

	if !slices.Contains(missing, "isolation.mode") {
		t.Fatalf("the rule holds with the field %q left out of the registry: %v", "isolation.mode", missing)
	}
}

// TestMT005EverySupportedKeyIsReadOutsideThisPackage is the second rule: a key
// marked supported is a key the code outside this package really applies, either
// through the expression the registry names or through the function it names for
// a key crewflow does whatever it says. A supported key nobody reads is a
// promise of the registry that the code does not keep (docs/DESIGN.md §5).
func TestMT005EverySupportedKeyIsReadOutsideThisPackage(t *testing.T) {
	sources := sourcesOutsideConfig(t)
	for _, setting := range supportedWithoutCode(settings, sources) {
		t.Errorf("the key %q is supported and %q is in no code outside this package", setting.Key, setting.Applies)
	}
}

// TestMT005FailsWhenTheCodeIsNowhere shows the rule failing: the key of the
// registry points at an expression that the code has not got, and the test has
// to say so.
func TestMT005FailsWhenTheCodeIsNowhere(t *testing.T) {
	nowhere := make([]Setting, 0, len(settings))
	for _, setting := range settings {
		if setting.Key == "project.repo" {
			setting.Applies = "cfg.Project.NoSuchKey"
		}
		nowhere = append(nowhere, setting)
	}

	lost := supportedWithoutCode(nowhere, sourcesOutsideConfig(t))

	if len(lost) != 1 || lost[0].Key != "project.repo" {
		t.Fatalf("the rule holds with the only supported key pointing at nothing: %v", lost)
	}
}

// TestMT006TheRegistryNamesOnlyTasksThatExist is the third rule: every issue the
// registry names is a task of the list above, which is where the waiting of
// crewflow is written down. A registry that names an issue nobody waits for
// sends a person after a task that is not there.
func TestMT006TheRegistryNamesOnlyTasksThatExist(t *testing.T) {
	for _, task := range tasksOutsideTheList(settings, tasksOfTheRegistry) {
		t.Errorf("the registry names crewflow#%d, which is not a task of the list %v", task, tasksOfTheRegistry)
	}
}

// TestMT006FailsWhenAnIssueIsNotOnTheList shows the rule failing: the registry
// names an issue that is not a task of the milestone, and the test has to say so.
func TestMT006FailsWhenAnIssueIsNotOnTheList(t *testing.T) {
	absent := slices.Clone(settings)
	for i := range absent {
		unwritten := slices.Clone(absent[i].Unwritten)
		for j := range unwritten {
			if unwritten[j].Task == 55 {
				unwritten[j].Task = 555
			}
		}
		absent[i].Unwritten = unwritten
	}

	unknown := tasksOutsideTheList(absent, tasksOfTheRegistry)

	if !slices.Contains(unknown, 555) {
		t.Fatalf("the rule holds with an issue of nobody: %v", unknown)
	}
}

// TestEveryChoiceOfAKeyIsInTheRegistry holds the registry to the values the file
// itself allows: every value of a key that is a choice is either applied or
// refused, and neither of them is a value nobody wrote about.
func TestEveryChoiceOfAKeyIsInTheRegistry(t *testing.T) {
	for key, allowed := range choices {
		for _, value := range valuesMissingFrom(key, settings, allowed) {
			t.Errorf("the value %q of %s is allowed by the file and in no registry", value, key)
		}
	}
}

// keysOfTheFile is every key of crewflow.toml, walked out of the type of a
// Config: a key a person writes, such as `project.repo` or `gates[].name`.
func keysOfTheFile(t *testing.T) []string {
	t.Helper()
	return keysOf(reflect.TypeFor[Config](), "")
}

// keysOf walks a struct of the file and writes the key of every field of it, with
// the dots of the tables and the brackets of the lists.
func keysOf(structure reflect.Type, prefix string) []string {
	var keys []string
	for i := range structure.NumField() {
		field := structure.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
		if field.Anonymous {
			keys = append(keys, keysOf(field.Type, prefix)...)
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		switch {
		case field.Type.Kind() == reflect.Struct:
			keys = append(keys, keysOf(field.Type, key)...)
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct:
			keys = append(keys, keysOf(field.Type.Elem(), key+"[]")...)
		default:
			keys = append(keys, key)
		}
	}
	return keys
}

// fieldsMissingFrom are the fields of the file that no key of the registry covers.
// A key covers the fields of a table of its own, the way the file writes them:
// `executor.fallback` is the key of a list of executors and the key of the
// command of each of them at once.
func fieldsMissingFrom(fields []string, registry []Setting) []string {
	keys := make([]string, 0, len(registry))
	for _, setting := range registry {
		keys = append(keys, setting.Key)
	}
	var missing []string
	for _, field := range fields {
		if !slices.ContainsFunc(keys, func(key string) bool { return covers(key, field) }) {
			missing = append(missing, field)
		}
	}
	return missing
}

// covers reports whether the key of the registry is the field of the file or the
// table it belongs to: "executor.fallback" covers "executor.fallback[].command".
func covers(key, field string) bool {
	if key == field {
		return true
	}
	return strings.HasPrefix(field, key) &&
		(strings.HasPrefix(field[len(key):], ".") || strings.HasPrefix(field[len(key):], "["))
}

// supportedWithoutCode are the keys the registry calls supported that the code
// outside this package does not apply: neither the expression it names is in that
// code, nor a function of the name it ends with is declared there.
func supportedWithoutCode(registry []Setting, sources string) []Setting {
	var lost []Setting
	for _, setting := range registry {
		if setting.Status != Supported {
			continue
		}
		_, name, found := strings.Cut(setting.Applies, ".")
		if strings.Contains(sources, setting.Applies) {
			continue
		}
		if found && strings.Contains(sources, "func "+name+"(") {
			continue
		}
		lost = append(lost, setting)
	}
	return lost
}

// tasksOutsideTheList are the issues the registry names that are not in the list
// of tasks crewflow waits for.
func tasksOutsideTheList(registry []Setting, known []int) []int {
	var unknown []int
	for _, setting := range registry {
		for _, unwritten := range setting.Unwritten {
			if unwritten.Task != 0 && !slices.Contains(known, unwritten.Task) {
				unknown = append(unknown, unwritten.Task)
			}
		}
	}
	return unknown
}

// valuesMissingFrom are the values a key allows that the registry says nothing
// about, neither as applied nor as refused.
func valuesMissingFrom(key string, registry []Setting, allowed []string) []string {
	var setting Setting
	for _, candidate := range registry {
		if candidate.Key == key {
			setting = candidate
		}
	}
	var missing []string
	for _, value := range allowed {
		if slices.Contains(setting.Supported, value) {
			continue
		}
		refused := slices.ContainsFunc(setting.Unwritten, func(u Asked) bool { return u.Value == value })
		if !refused {
			missing = append(missing, value)
		}
	}
	return missing
}

// sourcesOutsideConfig is the code of the module outside this package: where a
// key marked supported has to be applied, and the only place the rule of the
// registry looks for it. Files of a test are not that place: a test that writes a
// setting down says nothing about what the program does with it.
func sourcesOutsideConfig(t *testing.T) string {
	t.Helper()
	root := rootOfTheModule(t)
	var sources strings.Builder
	for _, folder := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(filepath.Join(root, folder), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch {
			case entry.IsDir():
				if filepath.Base(path) == "config" && filepath.Base(filepath.Dir(path)) == "internal" {
					return filepath.SkipDir
				}
			case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
				code, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sources.Write(code)
			}
			return nil
		}); err != nil {
			t.Fatalf("read the code outside %s: %v", folder, err)
		}
	}
	return sources.String()
}

// rootOfTheModule is the folder that holds go.mod, found by walking up from the
// folder the tests run in: a path of a test does not name a way out of the
// worktree, and the rule of the registry has to hold in the tree the tests are
// run in.
func rootOfTheModule(t *testing.T) string {
	t.Helper()
	folder, err := os.Getwd()
	if err != nil {
		t.Fatalf("the folder of the tests: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(folder, "go.mod")); err == nil {
			return folder
		}
		parent := filepath.Dir(folder)
		if parent == folder {
			t.Fatal("no go.mod above the folder of the tests, want the root of the module")
		}
		folder = parent
	}
}
