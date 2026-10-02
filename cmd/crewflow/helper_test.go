package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
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
				repo: repo,
				home: t.TempDir(),
				// Every setting of the worktree and not one of them written out here: a
				// helper or a setting a test builds for itself is one of nobody (F-082,
				// F-085, §7i).
				environment: gitConfig(gitConfigOfAWorktree(t, store, api, project)),
				address:     "https://" + host + "/" + tc.path,
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

// TestTheCredentialsOfAPushOfTheRunNeverReachTheHelpersOfTheMachine is F-116 (#141): git
// asks the helpers of the system and of the user before the helper of crewflow, and
// `osxkeychain` of the system file of macOS is one of them — it has the token of the login
// of a person, it answers first, and after a push went through it is handed the token of the
// App of the run, which it writes down in the keychain of the owner (R6).
//
// A real git, the settings of git as crewflow hands them to a command, and a helper of the
// machine of the test that writes down everything it is asked: while those settings stand,
// git asks it nothing at all — not for the question of the push, not for the `store` of the
// credentials the push went through with, not for the `erase` of a rejection.
//
// The second case is the same git with the same machine and no settings of crewflow, and it
// is what makes the first one worth something: a helper that is asked nothing is also a
// helper git would never have asked, and this case says that this chain does reach it.
// (docs/DESIGN.md §7i)
func TestTheCredentialsOfAPushOfTheRunNeverReachTheHelpersOfTheMachine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	cases := []struct {
		name string
		// settings is whether the push is given the settings of git of crewflow. Without
		// them the chain of the helpers of the machine is the chain of the push.
		settings bool
		// answer is the password git is given for the repository of the project: the token
		// of the App of the run where the helper of crewflow is the only helper, and the
		// password of the machine where nothing of crewflow is in the chain.
		answer string
		// asked is whether git asks the helper of the machine at all.
		asked bool
	}{
		{name: "the settings of crewflow", settings: true, answer: "password=ghs_token_of_the_run"},
		{name: "the helpers of the machine alone", answer: "password=password_of_the_machine", asked: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &storeOfTheTest{key: keyOfTheTest(t)}
			api := useMachineOfTheTest(t, store, nil)
			api.demandsPassword = true
			host := hostOfTest(api)
			repo := t.TempDir()
			project := writeTheFileOfTheProject(t, repo, projectConfig+botConfig+
				"\n[forge]\nkind = \"github\"\nhost = \""+host+"\"\n")
			asked := filepath.Join(t.TempDir(), "asked.txt")
			on := gitOfTheTest{
				repo: repo,
				home: t.TempDir(),
				// The helper of the machine of the test stands in the file of the user
				// where `osxkeychain` of macOS stands in the file of the system: the order
				// of the helpers is the same, and the keychain of the machine is not a
				// thing a test may touch (§7i).
				global:  writeTheHelperOfTheMachine(t, asked),
				address: "https://" + host + "/naghuale/crewflow.git",
				machine: map[string]string{
					helperStoreVar:  writeTheKeyOfTheTest(t, store),
					helperServerVar: writeTheCertificateOfTheTest(t, api),
					helperSaidVar:   filepath.Join(t.TempDir(), "said.txt"),
				},
			}
			if tc.settings {
				on.environment = gitConfig(gitConfigOfAWorktree(t, store, api, project))
			}
			git(t, on, "init", "--quiet")

			filled := git(t, on, "credential", "fill")
			if !strings.Contains(filled, tc.answer) {
				t.Errorf("git credential fill wrote %q, want %q", filled, tc.answer)
			}
			// The other two commands of the credentials: what a push went through with is
			// handed to the helpers to keep (`store`) and to forget (`erase`) — that is
			// the way the token of an hour of the App reached the keychain of the owner.
			// Whether git hands it over at all is git's own business; what this test holds
			// is that the settings of crewflow leave the helper of the machine nothing to
			// hand it to.
			git(t, on, "credential", "approve")
			git(t, on, "credential", "reject")

			if said := readTheFileIfItIsThere(t, asked); (said != "") != tc.asked {
				t.Errorf("the helper of the machine was asked %q, want it asked: %t", said, tc.asked)
			}
		})
	}
}

// writeTheHelperOfTheMachine writes a helper of the machine of the test and the file of the
// settings of that machine that names it, and answers with the file: git asks the helpers of
// the settings of the system and of the user before the helper of the worktree, and a test of
// this needs one of them it can watch (§7i, #141).
func writeTheHelperOfTheMachine(t *testing.T, record string) string {
	t.Helper()
	folder := t.TempDir()
	helper := filepath.Join(folder, "helper-of-the-machine.sh")
	script := "#!/bin/sh\n" +
		"asked=$(cat)\n" +
		"{ echo \"operation: $*\"; echo \"asked: $asked\"; } >> '" + record + "'\n" +
		"echo 'username=x-access-token'\n" +
		"echo 'password=password_of_the_machine'\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatalf("write the helper of the machine: %v", err)
	}
	settings := filepath.Join(folder, "gitconfig")
	if err := os.WriteFile(settings, []byte("[credential]\n\thelper = "+helper+"\n"), 0o600); err != nil {
		t.Fatalf("write the settings of the machine: %v", err)
	}
	return settings
}

// readTheFileIfItIsThere is what a file of the test holds, or an empty string where there is
// no file at all: git asks a helper by running it, so a file that was never written is the
// whole of what the helper was asked (§7i).
func readTheFileIfItIsThere(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
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
// the settings of git as the environment of the command carries them, the file of the
// settings of the machine and what the helper of the test is to work against.
type gitOfTheTest struct {
	repo    string
	home    string
	address string
	global  string
	machine map[string]string
	// environment is the settings of git as crewflow hands them to a command of it: the
	// pairs `GIT_CONFIG_COUNT` and `GIT_CONFIG_KEY_n` with `GIT_CONFIG_VALUE_n`, which
	// hold for that one command and for nothing else (§7a, §7i).
	environment []string
}

// git is the real git with the settings of the worktree and nothing else: the helpers of
// the machine are reset before crewflow's own, the way the push of a merge resets them,
// and the answer of a question is the one of crewflow's helper.
//
// It answers what git wrote and what it failed on, and it does not stop the test at the
// first failure: a git that could not get the credentials is the failure these tests are
// about, and the test has to be able to read it (docs/DESIGN.md §7i).
func git(t *testing.T, on gitOfTheTest, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = on.repo
	command.Stdin = strings.NewReader("url=" + on.address + "\n\n")
	command.Env = append(withoutGitOfTheMachine(),
		"HOME="+on.home, "XDG_CONFIG_HOME="+on.home,
		// The settings of the system of the machine are not a file of a test, and the
		// helper they name on macOS — `osxkeychain` — is one question away from the
		// keychain of the person who runs the test. A helper of a test is asked instead,
		// and it is named in the file of the settings the test wrote (§7i).
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL="+globalOfTheTest(on),
		// The certificate of the server of the test is not one git is asked to trust:
		// what has to trust it is the helper of the credentials, and it does.
		"GIT_SSL_NO_VERIFY=true")
	command.Env = append(command.Env, on.environment...)
	for name, value := range on.machine {
		command.Env = append(command.Env, name+"="+value)
	}
	out, _, err := runOfTheCommand(command)
	if err != nil && len(out[0]) == 0 && len(out[1]) == 0 {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), on.repo, err)
	}
	return string(out[0])
}

// globalOfTheTest is the file of the settings of the user of a test: the one it wrote, and
// no settings at all where the test wrote none, which git reads as an empty file.
func globalOfTheTest(on gitOfTheTest) string {
	if on.global == "" {
		return "/dev/null"
	}
	return on.global
}

// settingsOf are the settings of the machine of a test as the environment of a command of
// git carries them: `GIT_CONFIG_COUNT` and a pair for every setting, which is how §7a has
// git read the settings of a project — and how a test gives its own git the name of its
// committer instead of writing them into a file of the machine.
func settingsOf(settings map[string]string) []string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	environment := []string{fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(keys))}
	for i, key := range keys {
		environment = append(environment,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, key),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, settings[key]))
	}
	return environment
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
