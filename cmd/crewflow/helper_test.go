package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestGitAsksCrewflowForTheCredentialsOfThisRepository is the whole of a push in one
// command of a test: a real git, the settings of git exactly as the adapter of GitHub
// writes them for a worktree, and this binary — which is crewflow whenever git starts it
// as `auth git-credential`. The question git asks is the one git writes for the address of
// the repository, and the answer has to come back through git in the form git reads.
//
// The two halves of this were right separately and the push was still refused: git names
// the repository in its question only when `credential.useHttpPath` is set (F-085), and it
// writes the name as the host holds it — `naghuale/crewflow.git` for the address of a
// repository on GitHub — which the helper did not recognise as its project and answered
// nothing to, and git asked a person for a password instead (F-087, 01.10.2026). Both are
// in the settings of the adapter and in the name the helper compares, and the only test
// that would have shown either is this one.
func TestGitAsksCrewflowForTheCredentialsOfThisRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	cases := []struct {
		name string
		// path is the repository in the address git is given, which is the form a host
		// holds it in and not the form the file of the project is written in.
		path string
		want string
	}{
		{
			name: "the address of the repository on GitHub",
			path: "naghuale/crewflow.git",
			want: "password=ghs_token_of_the_run",
		},
		{
			name: "the address without the suffix the host adds",
			path: "naghuale/crewflow",
			want: "password=ghs_token_of_the_run",
		},
		{
			name: "the address with a slash at the end",
			path: "naghuale/crewflow/",
			want: "password=ghs_token_of_the_run",
		},
		{
			name: "another repository of the same host",
			path: "naghuale/telecli",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &storeOfTheTest{key: keyOfTheTest(t)}
			api := useMachineOfTheTest(t, store, nil)
			api.demandsPassword = true
			host := hostOfTest(api)
			// The helper is started in the folder of the repository and reads the file of
			// the project of that folder, which is what a person who runs crewflow there
			// does.
			repo := t.TempDir()
			project := writeTheFileOfTheProject(t, repo, projectConfig+botConfig+
				"\n[forge]\nkind = \"github\"\nhost = \""+host+"\"\n")
			said := filepath.Join(t.TempDir(), "said.txt")
			on := gitOfTheTest{
				repo:    repo,
				home:    t.TempDir(),
				address: "https://" + host + "/" + tc.path,
				// Every setting of the worktree and not one of them written out here: a
				// helper or a setting a test builds for itself is one of nobody (F-082,
				// F-085, §7i).
				settings: gitConfigOfAWorktree(t, store, api, project),
				machine: map[string]string{
					helperStoreVar:  writeTheKeyOfTheTest(t, store),
					helperServerVar: writeTheCertificateOfTheTest(t, api),
					helperSaidVar:   said,
				},
			}
			git(t, on, "init", "--quiet")

			filled := git(t, on, "credential", "fill")

			// The answer of the helper has to reach git in the form git reads, and git
			// has nothing else to ask about: a helper that stays quiet leaves git with
			// no password at all, which is what a push of another project has to meet.
			if answered := strings.Contains(filled, "password="); answered != (tc.want != "") {
				t.Errorf("git credential fill wrote %q, want the answer of the helper: %q", filled, tc.want)
			}
			// The question is git's own, and it is what the helper is judged by: what
			// crewflow named in the settings made git name the repository here, and with
			// the form the host holds it in (F-085, F-087).
			asked := readTheFile(t, said)
			for _, want := range []string{"protocol=https", "host=" + host, "started with ["} {
				if !strings.Contains(asked, want) {
					t.Errorf("the helper was asked %s, want it to hold %q", headOf(asked), want)
				}
			}
			// The question names the repository, which is what the setting of the worktree
			// is for (F-085), and names it in the form the host holds it in (F-087).
			named := "path=naghuale/" + strings.TrimSuffix(strings.TrimPrefix(tc.path, "naghuale/"), "/")
			for _, want := range []string{"get]", named} {
				if !strings.Contains(asked, want) {
					t.Errorf("the helper was asked %s, want git to have named %q", headOf(asked), want)
				}
			}
			// The helper was asked and answered with 0 even where it says nothing: a
			// helper that keeps quiet about a repository that is not its project is the
			// whole of what it is for (§7i).
			if !strings.Contains(asked, "answered with 0") {
				t.Errorf("the helper was asked %s, want it to have answered with 0", headOf(asked))
			}
			if !strings.Contains(asked, tc.want) && tc.want != "" {
				t.Errorf("the helper answered %s, want %q for a push of this project", headOf(asked), tc.want)
			}
			// The token is signed for the App of the project: a helper that answered out
			// of nothing would have asked nobody for one.
			if tc.want != "" && len(api.asked) == 0 {
				t.Error("the helper of this build asked nobody for a token, want the api of the test")
			}
		})
	}
}

// TestTheHelperOfThisBuildSaysWhyItCannotSignWithoutAKey is the same helper on a machine
// whose store holds no key of the App: it has to say why, because git says nothing about a
// helper it could not ask, and the words are what a person has to act on
// (docs/DESIGN.md §7i).
func TestTheHelperOfThisBuildSaysWhyItCannotSignWithoutAKey(t *testing.T) {
	store := &storeOfTheTest{}
	api := useMachineOfTheTest(t, store, nil)
	host := hostOfTest(api)
	repo := t.TempDir()
	writeTheFileOfTheProject(t, repo, projectConfig+botConfig+
		"\n[forge]\nkind = \"github\"\nhost = \""+host+"\"\n")

	// Started the way git starts it: the operation after the flags of the command.
	command := exec.Command(os.Args[0], helperOfGit, helperItself, "-as", "executor", "get")
	command.Dir = repo
	command.Env = append(os.Environ(),
		helperStoreVar+"=",
		helperServerVar+"="+writeTheCertificateOfTheTest(t, api),
		"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir())
	command.Stdin = strings.NewReader("protocol=https\nhost=" + host +
		"\npath=naghuale/crewflow.git\n\n")
	out, code, _ := runOfTheCommand(command)

	if code == 0 {
		t.Fatalf("the helper without a key answered and came out with 0, want a refusal (stderr: %q)", out[1])
	}
	if !strings.Contains(string(out[1]), "key") {
		t.Errorf("the helper said %q, want it to say that it has no key of the app", out[1])
	}
}

// writeTheFileOfTheProject writes the file of the project into the folder of the
// repository of a test, under the name the helper reads it by: `./crewflow.toml` of the
// folder the helper was started in.
func writeTheFileOfTheProject(t *testing.T, folder, content string) string {
	t.Helper()
	path := filepath.Join(folder, "crewflow.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write the file of the project: %v", err)
	}
	return path
}

// writeTheKeyOfTheTest puts the key of the app of the test in a file of the test and says
// where it is: a helper of a test reads it there, and the keychain of the machine is
// never in a test (docs/DESIGN.md §7i).
func writeTheKeyOfTheTest(t *testing.T, store *storeOfTheTest) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, store.key, 0o600); err != nil {
		t.Fatalf("write the key of the test: %v", err)
	}
	return path
}

// writeTheCertificateOfTheTest puts the certificate of the server of the test in a file of
// the test, so that a helper of a test trusts that server and no other.
func writeTheCertificateOfTheTest(t *testing.T, api *apiOfTheTest) string {
	t.Helper()
	certificate := api.server.Certificate()
	if certificate == nil {
		t.Fatal("the server of the test holds no certificate")
	}
	if _, err := x509.ParseCertificate(certificate.Raw); err != nil {
		t.Fatalf("the certificate of the test is not one: %v", err)
	}
	path := filepath.Join(t.TempDir(), "certificate.pem")
	if err := os.WriteFile(path,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatalf("write the certificate of the test: %v", err)
	}
	return path
}

// readTheFile is what a file of the test holds, for a test that looks at what the helper
// of it was asked and what it answered.
func readTheFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// headOf is the first line of what a helper of a test said, for a test that reads it: a
// refusal of a helper is a line and not the usage of every command of crewflow with it.
func headOf(said string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(said), "\n")
	if len(line) > 120 {
		return line[:120] + "…"
	}
	return line
}

// gitConfigOfAWorktree is every setting of git that a worktree of a run is given, as the
// adapter of GitHub writes them: the helper of the credentials and the setting that makes
// git name the repository in the question it asks it. They are taken out of the adapter
// and not written out here a second time — F-082, F-085 and F-087 are all about these
// lines (docs/DESIGN.md §7i).
func gitConfigOfAWorktree(t *testing.T, store secret.Store, api *apiOfTheTest, configPath string) map[string]string {
	t.Helper()
	env := forge.Env{
		ConfigPath: configPath,
		Secrets:    store,
		HTTP:       api.server.Client(),
		Now:        func() time.Time { return time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC) },
	}
	adapter := github.New("naghuale/crewflow", hostOfTest(api), env).
		WithBot(github.Bot{AppID: 5107052, InstallationID: 12345, DefaultBranch: "main"})
	identity, err := adapter.ExecutorIdentity(t.Context())
	if err != nil {
		t.Fatalf("the identity of the executor of the project: %v", err)
	}
	settings := identity.GitConfig
	if helper := settings["credential.helper"]; !strings.HasPrefix(helper, "!") {
		t.Fatalf("credential.helper = %q, want it to begin with %q", helper, "!")
	}
	if settings["credential.useHttpPath"] != "true" {
		t.Errorf("the worktree is given %v, want git to be told to name the repository in its question: "+
			"without that the helper is asked about a repository it is to answer for", settings)
	}
	return settings
}

// gitOfTheTest is the machine a git of a test runs in: the folder of the repository, a
// home of its own, the address of the repository of the project on the host of the test,
// the settings of the worktree and what the helper of the test is to work against.
type gitOfTheTest struct {
	repo     string
	home     string
	address  string
	settings map[string]string
	machine  map[string]string
}

// git is the real git with the settings of the worktree and nothing else: the helpers of
// the machine are reset before crewflow's own, the way the push of a merge resets them,
// and the answer below is the one of crewflow's helper.
//
// It answers what git wrote and what it failed on, and it does not stop the test at the
// first failure: a git that could not get the credentials is the failure these tests are
// about, and the test has to be able to read it (docs/DESIGN.md §7i).
func git(t *testing.T, on gitOfTheTest, args ...string) string {
	t.Helper()
	command := exec.Command("git", append(flagsOfTheTest(on), args...)...)
	command.Dir = on.repo
	command.Stdin = strings.NewReader("url=" + on.address + "\n\n")
	command.Env = append(withoutGitOfTheMachine(),
		"HOME="+on.home, "XDG_CONFIG_HOME="+on.home,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		// The certificate of the server of the test is not one git is asked to trust:
		// what has to trust it is the helper of the credentials, and it does.
		"GIT_SSL_NO_VERIFY=true")
	for name, value := range on.machine {
		command.Env = append(command.Env, name+"="+value)
	}
	out, _, err := runOfTheCommand(command)
	if err != nil && len(out[0]) == 0 && len(out[1]) == 0 {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), on.repo, err)
	}
	return string(out[0])
}

// flagsOfTheTest are the settings of the worktree as the command line of git takes them:
// every one of them, in the order of their names, and nothing of the machine (F-082, §7i).
func flagsOfTheTest(on gitOfTheTest) []string {
	names := make([]string, 0, len(on.settings))
	for name := range on.settings {
		names = append(names, name)
	}
	slices.Sort(names)
	flags := make([]string, 0, 2*len(names))
	for _, name := range names {
		flags = append(flags, "-c", name+"="+on.settings[name])
	}
	return flags
}

// withoutGitOfTheMachine is the environment of a test with the settings of the repository
// of the machine taken out. A test of a push must not find the checkout of the person in
// `GIT_DIR`: git then starts the helper of the credentials in that checkout, the helper
// reads `./crewflow.toml` of the worktree of the person, and a test of this project
// answers — or refuses — for the project of another worktree (docs/DESIGN.md §7i).
func withoutGitOfTheMachine() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

// runOfTheCommand starts a command of a test and says what it wrote on each of its two
// streams and the code it came out with, without stopping at the first failure: the helper
// of a test is refusing, and a refusal is what the test is reading.
func runOfTheCommand(command *exec.Cmd) ([2][]byte, int, error) {
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return [2][]byte{[]byte(stdout.String()), []byte(stderr.String())},
		command.ProcessState.ExitCode(), err
}
