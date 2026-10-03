package doctor

import (
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/buildinfo"
)

// TestTheReportSaysWhatThisBuildIs holds the first line of §5: a report of a machine says
// which program is on it — the version, the commit it was built from and the capabilities
// its code has — because the whole of §5 is a project relying on a mechanism of it, and a
// person cannot check that without being told what the mechanism would be (F-178).
func TestTheReportSaysWhatThisBuildIs(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	build := checkOf(t, report, buildCheck)
	if build.Status != OK {
		t.Errorf("check %q = %q (%s), want it to pass", buildCheck, build.Status, build.Detail)
	}
	for _, want := range append([]string{"crewflow ", "commit"}, buildinfo.Capabilities()...) {
		if !strings.Contains(build.Detail, want) {
			t.Errorf("check %q detail = %q, want it to mention %q", buildCheck, build.Detail, want)
		}
	}
}

// TestTheReportFailsWhereTheBuildHasNotWhatTheProjectRequires is F-178 as a report of it: a
// project that requires a mechanism the installed build has not got is a machine crewflow
// cannot work on, and a report that said nothing of it would leave the person to find it
// out from a command that refuses in the middle of a task.
func TestTheReportFailsWhereTheBuildHasNotWhatTheProjectRequires(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
	config := writeConfig(t, baseConfig+
		"\n[crewflow]\nrequires = [\"parallel-admission\", \"mechanism-of-another-build\"]\n")

	report := runOn(t, m, config)

	build := checkOf(t, report, buildCheck)
	if build.Status != Fail {
		t.Errorf("check %q = %q (%s), want it to fail", buildCheck, build.Status, build.Detail)
	}
	for _, want := range []string{buildinfo.ReasonCapabilityMissing, "mechanism-of-another-build"} {
		if !strings.Contains(build.Detail, want) {
			t.Errorf("check %q detail = %q, want it to mention %q", buildCheck, build.Detail, want)
		}
	}
	if !strings.Contains(build.Hint, "install a build") {
		t.Errorf("check %q hint = %q, want it to say what to do about it", buildCheck, build.Hint)
	}
	if report.OK() {
		t.Error("report.OK() = true, want false: the project needs a mechanism this machine has not")
	}
}

// TestTheReportSaysWhatTheProjectRequiresOfTheBuild keeps the answer whole: what the project
// needs is one of the things a person reads here, and a report that named only what the
// build has would leave the check of the file of the project to be done by hand.
func TestTheReportSaysWhatTheProjectRequiresOfTheBuild(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
	config := writeConfig(t, baseConfig+"\n[crewflow]\nrequires = [\"parallel-admission\"]\n")

	report := runOn(t, m, config)

	build := checkOf(t, report, buildCheck)
	if !strings.Contains(build.Detail, "the project requires parallel-admission") {
		t.Errorf("check %q detail = %q, want it to name what the project requires of the build", buildCheck, build.Detail)
	}
}

// TestTheReportSaysTheLagOfTheBuildFromMain is the other half of F-178: a build that is
// behind `main` has not a mechanism `main` has, and nothing else on the machine says so.
// The question is asked of the repository the build was made from and of nothing else — no
// network, no fetch — and where the answer is not to be had the report says `unknown` and
// not that the build is up to date.
func TestTheReportSaysTheLagOfTheBuildFromMain(t *testing.T) {
	cases := []struct {
		name string
		// holds is whether the repository is the one the build was made from: the commit
		// the build names is an object of it.
		holds bool
		// main is what `git rev-parse` answers for the tip of `main`, and an empty string
		// is a repository that has no such ref — a machine without a fetch, which is what
		// the question of the lag looks like most of the time.
		main string
		// ancestor is what `git merge-base --is-ancestor` says about the build and the tip
		// of `main`, and count is what `git rev-list --count` answers between them.
		ancestor bool
		count    string
		status   Status
		want     []string
		// absent is whether the check of the lag is not made at all: a report that made it
		// in a repository that is not the one the build was made from would answer
		// `unknown` about a machine it never asked.
		absent bool
	}{
		{
			name:   "up to date",
			holds:  true,
			main:   "deadbeef",
			status: OK,
			want:   []string{"up to date", "deadbeef"},
		},
		{
			name:     "behind main",
			holds:    true,
			main:     "7f2a1c9",
			ancestor: true,
			count:    "3\n",
			status:   Warn,
			want:     []string{"behind main by 3 commits", "deadbeef", "7f2a1c9"},
		},
		{
			name:   "no main in this repository",
			holds:  true,
			main:   "",
			status: Warn,
			want:   []string{"unknown", "refs/remotes/origin/main"},
		},
		{
			name:     "not on main at all",
			holds:    true,
			main:     "7f2a1c9",
			ancestor: false,
			status:   Warn,
			want:     []string{"unknown", "deadbeef"},
		},
		{
			name:   "a repository that is not the one the build was made from",
			holds:  false,
			main:   "7f2a1c9",
			absent: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stampedCommit(t, "deadbeef")
			m := newMachine().has("git", "gh", "agent").
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
			if tc.holds {
				m.prints("git cat-file -e deadbeef^{commit}", "")
			} else {
				m.fails("git cat-file -e deadbeef^{commit}", "fatal: Not a valid object name deadbeef")
			}
			if tc.main == "" {
				m.fails("git rev-parse --verify -q refs/remotes/origin/main", "")
			} else {
				m.prints("git rev-parse --verify -q refs/remotes/origin/main", tc.main+"\n")
			}
			if tc.holds && tc.main != "" && tc.main != "deadbeef" {
				line := "git merge-base --is-ancestor deadbeef " + tc.main
				if tc.ancestor {
					m.prints(line, "")
					m.prints("git rev-list --count deadbeef.."+tc.main, tc.count)
				} else {
					m.fails(line, "")
				}
			}
			config := writeConfig(t, baseConfig)

			report := runOn(t, m, config)

			lag, made := checkByName(report, mainCheck)
			if tc.absent {
				if made {
					t.Errorf("the report holds the check %q (%s), want none: the question was not asked",
						mainCheck, lag.Detail)
				}
				build := checkOf(t, report, buildCheck)
				if !strings.Contains(build.Detail, "unknown") {
					t.Errorf("check %q detail = %q, want it to say that the lag from main is unknown",
						buildCheck, build.Detail)
				}
				return
			}
			if !made {
				t.Fatalf("the report holds no check %q, want the answer of the question (checks: %v)",
					mainCheck, checkNames(report))
			}
			if lag.Status != tc.status {
				t.Errorf("check %q = %q (%s), want %q", mainCheck, lag.Status, lag.Detail, tc.status)
			}
			for _, want := range tc.want {
				if !strings.Contains(lag.Detail, want) {
					t.Errorf("check %q detail = %q, want it to mention %q", mainCheck, lag.Detail, want)
				}
			}
		})
	}
}

// TestTheBuildWithoutACommitIsNotComparedWithMain is the case of every build made without
// the link-time stamp: there is no commit to compare, and a report that compared what it
// does not have would say the build is up to date with a main nobody looked at.
func TestTheBuildWithoutACommitIsNotComparedWithMain(t *testing.T) {
	stampedCommit(t, "unknown")
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	if lag, made := checkByName(report, mainCheck); made {
		t.Errorf("the report holds the check %q (%s), want none: there is no commit to compare",
			mainCheck, lag.Detail)
	}
	if !strings.Contains(checkOf(t, report, buildCheck).Detail, "commit unknown") {
		t.Errorf("check %q detail = %q, want it to show that the build says no commit",
			buildCheck, checkOf(t, report, buildCheck).Detail)
	}
}

// TestTheReportAsksGitNothingAboutTheNetwork keeps the check of the build local: the tip of
// `main` is read out of the repository this machine has, and nothing of it is fetched — a
// report that went to the network would make every check of a machine wait for a route
// (docs.DESIGN.md §7d).
func TestTheReportAsksGitNothingAboutTheNetwork(t *testing.T) {
	stampedCommit(t, "deadbeef")
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "  ✓ Logged in to github.com account octocat\n").
		prints("git cat-file -e deadbeef^{commit}", "").
		prints("git rev-parse --verify -q refs/remotes/origin/main", "deadbeef\n")
	config := writeConfig(t, baseConfig)

	runOn(t, m, config)

	for _, ran := range m.ran {
		line := strings.Join(ran.args, " ")
		for _, fetch := range []string{"fetch", "pull", "remote update", "ls-remote", "push"} {
			if strings.Contains(line, fetch) {
				t.Errorf("the report ran `git %s`, want nothing that reaches the network", line)
			}
		}
	}
}

// stampedCommit makes the build this test runs as one that says which commit it was made
// from, the way the release of a version stamps it: a build of `go build` alone says
// `unknown`, and a comparison with main needs a commit to compare (docs.DESIGN.md §5).
func stampedCommit(t *testing.T, commit string) {
	t.Helper()
	was := buildinfo.Commit
	t.Cleanup(func() { buildinfo.Commit = was })
	buildinfo.Commit = commit
}
