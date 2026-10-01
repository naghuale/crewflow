package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestGitAsksCrewflowForTheCredentialsAndGetsItsToken is the two halves of a push of this
// project, wired together the way a machine wires them: a real git, and a real crewflow —
// this binary, which is crewflow whenever git starts it as `auth git-credential` — behind
// the setting of the helper taken out of the adapter, the very line crewflow writes for a
// worktree of a run.
//
// What the two halves say to each other is not something crewflow decides: git adds the
// operation of the credentials to the end of the line of the helper, after the flags of
// the command, and a helper that reads the operation only as the word before them answers
// `unexpected argument "get"` to the very push it exists to sign (F-085, 01.10.2026). Every
// part of it was right before — a helper that answers, a git that calls one, a key in a
// store — and the push was still refused, because the two had never been in one test.
func TestGitAsksCrewflowForTheCredentialsAndGetsItsToken(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	// The host of a project asks a git that comes without credentials for a password,
	// and git asks the helper only after such an answer: a server that says nothing
	// about credentials is a server that is never asked for any.
	api.demandsPassword = true
	host := hostOfTest(api)
	// The helper is started in the folder of the repository and reads the file of the
	// project of that folder, which is what a person who runs crewflow there does.
	repo := t.TempDir()
	project := writeTheFileOfTheProject(t, repo, projectConfig+botConfig+
		"\n[forge]\nkind = \"github\"\nhost = \""+host+"\"\n")
	said := filepath.Join(t.TempDir(), "said.txt")
	on := gitOfTheTest{
		repo:   repo,
		home:   t.TempDir(),
		host:   host,
		helper: helperOfAWorktree(t, store, api, project),
		machine: map[string]string{
			helperStoreVar:  writeTheKeyOfTheTest(t, store),
			helperServerVar: writeTheCertificateOfTheTest(t, api),
			helperSaidVar:   said,
		},
	}

	// The folder of the test is a repository of its own: git starts the helper of the
	// credentials in the top of the repository it works in, and a folder of a test that
	// sits inside another repository would have the helper read the file of the project
	// of that other one (docs/DESIGN.md §7i).
	git(t, on, "init", "--quiet")

	// `ls-remote` is what a push asks first, and it asks for the credentials of the
	// repository before it connects at all: the helper is asked, answers, and git goes on
	// to the host, which is where the server of a test is not a git.
	_, failure := git(t, on, "ls-remote", "https://"+host+"/naghuale/crewflow")

	// Git got past the credentials of the push. A git that has none asks a person for a
	// password and says so, and that is the one failure this test is about: without a word
	// of it git took what the helper answered and went on to the host.
	for _, unwanted := range []string{"Username", "username", "Terminal", "Device not configured"} {
		if strings.Contains(failure, unwanted) {
			t.Errorf("git wrote %q, want it to get past the credentials of the push", failure)
		}
	}
	asked := readTheFile(t, said)
	for _, want := range []string{
		// The request of a push of this project, as git writes it to the helper.
		"protocol=https",
		"host=" + host,
		"path=naghuale/crewflow",
		// The answer, in the format git reads: a name of a user and a password.
		"username=x-access-token\npassword=ghs_token_of_the_run\n",
		// The operation, where git puts it — at the end, after the flags of the command.
		"started with [auth git-credential -as executor get]",
		"answered with 0",
	} {
		if !strings.Contains(asked, want) {
			t.Errorf("the helper of this build said %s, want it to hold %q", headOf(asked), want)
		}
	}
	// The password is a token signed for the App of the project and nothing written out
	// of nothing: the API of the test is the only one a helper of a test can have asked.
	if len(api.asked) == 0 {
		t.Error("the helper of this build asked nobody for a token, want the api of the test")
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
	command.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\npath=naghuale/crewflow\n\n")
	out, code, _ := runOfTheCommand(command)
	stderr := string(out[1])

	if code == 0 {
		t.Fatalf("the helper without a key answered and came out with 0, want a refusal (stderr: %q)", stderr)
	}
	if !strings.Contains(stderr, "key") {
		t.Errorf("the helper said %q, want it to say that it has no key of the app", stderr)
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

// helperOfAWorktree is the setting of `credential.helper` of a worktree of a run, as the
// adapter of GitHub writes it: `!<this build> auth git-credential -as executor`. It is
// taken out of the adapter and not written out here a second time, because a helper a
// test builds for itself is a helper of nobody — and the line is what F-082 and F-085 are
// about (docs/DESIGN.md §7i).
func helperOfAWorktree(t *testing.T, store secret.Store, api *apiOfTheTest, configPath string) string {
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
	helper := identity.GitConfig["credential.helper"]
	if helper == "" {
		t.Fatal("the adapter named no helper of the credentials, want the one of this build")
	}
	if !strings.HasPrefix(helper, "!") {
		t.Fatalf("credential.helper = %q, want it to begin with %q", helper, "!")
	}
	return helper
}

// gitOfTheTest is the machine a git of a test runs in: the folder of the repository, a
// home of its own, the host of the project, the line of the helper of the worktree and
// what the helper of the test is to work against.
type gitOfTheTest struct {
	repo    string
	home    string
	host    string
	helper  string
	machine map[string]string
}

// git is the real git, with the helper of the worktree set to the line the adapter writes
// and nothing else: the helpers of the machine are reset before crewflow's own, the way
// the push of a merge resets them, and the answer below is the one of crewflow's helper.
//
// It answers what git wrote and what it failed on, and it does not stop the test at the
// first failure: a git that could not get the credentials is the failure this test is
// about, and the test has to be able to read it (docs/DESIGN.md §7i).
func git(t *testing.T, on gitOfTheTest, args ...string) (string, string) {
	t.Helper()
	command := exec.Command("git", append(append([]string{
		"-c", "credential.helper=", "-c", "credential.helper=" + on.helper,
		// Git names the repository in the question it asks the helper only when it is
		// told to (`credential.useHttpPath`), and the helper of crewflow answers only
		// for a push of its own project — it has to know which repository the push is
		// of. The setting is given here as the machine of a push has to give it, and
		// nothing else: a git that does not know the repository asks, and the helper
		// stays quiet, which is what it does for a push of another project.
		"-c", "credential.useHttpPath=true",
	}, args...), nil...)...)
	command.Dir = on.repo
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
	return string(out[0]), string(out[1])
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
