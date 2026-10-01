# crewflow

**A fail-closed coordination protocol for coding agents: responsibilities are explicit, approvals
are bound to a commit SHA, secrets stay out of reach, and a merge is verified before it happens.**

An executor implements one task in its own git worktree. An orchestrator reviews the result and
decides what gets merged. Issues and pull requests remain the source of truth, approvals are
bound to a specific commit SHA, and a merge is a fast-forward of exactly that commit on green CI.

```
Issue → Run → Pull request → Review → Approval(SHA) → Merge gate → Verify
```

## Why

Most agent workflows optimise autonomy. crewflow optimises coordination:

- one task = one worktree;
- one approval = one commit SHA;
- one merge = one verified fast-forward;
- the executor and the orchestrator have different responsibilities;
- git hosting stays the source of truth;
- paid CI is a budget planned before a run, not a free check — a private project once spent a
  month of GitHub Actions minutes on re-runs of reworked pull requests.

The goal is not to replace the orchestrator. The goal is agent collaboration that follows explicit
rules, boundaries and responsibilities, on any project, with no loss of quality.

## What crewflow is not

- not a coding agent: it writes no code itself;
- not a CI replacement: it reads the result of CI;
- not a workflow database or a multi-user service: one person, one orchestrator, one machine.

It is project-, agent- and language-agnostic: the gates, requirements and executor are plain
commands in `crewflow.toml`.

## Status

Used daily on real projects (telecli, a Telegram client for the terminal, and crewflow itself).

Works today:

- ✅ a worktree per task, a headless executor, a named outcome for every run
- ✅ the review → changes requested → continue cycle, in the same session
- ✅ tasks as readable specs, with boundaries and owner approval for risky ones
- ✅ the executor reads the dependencies the project names and never a secret
- ✅ two identities for the executor, `owner` or a GitHub App `bot`, shown in every report
- ✅ a run that stands is visible: `stalled` with the length of the silence in `task list`,
  an event in the journal of the attempt and one record under the task per episode
- ✅ two identities for the orchestrator too, `shared` or a second GitHub App, and a gate that
  counts the records of the owner apart from the records of the orchestrator
- ✅ the merge gate in code: `crewflow review` says whether a change may be merged, and which
  one of the reasons of §7h says why it may not
- ✅ the owner's acceptance: a task marked `owner-check` is not merged until the owner writes
  `ACCEPTED <sha of the head>` under its PR
- ✅ the merge itself: `crewflow merge` fast-forwards the branch to exactly the approved
  commit and proves it with `git ls-remote`; `crewflow verify` checks the branch, the
  issue and the CI of it afterwards

In progress:

- ⏳ `crewflow init` and recovery of an interrupted task

The design and the plan are in [docs/DESIGN.md](docs/DESIGN.md) (in Russian). The steps of the
process itself, in order, are in [CREWFLOW_CHECKLIST.md](CREWFLOW_CHECKLIST.md) (in Russian): every
item links to the rule it comes from.

## Install

Requires Go 1.27 or later and the [GitHub CLI](https://cli.github.com/) (`gh`), logged in.

```sh
go install github.com/naghuale/crewflow/cmd/crewflow@latest
```

A run in the mode of the `bot` reads the key of the App from the macOS keychain, and the keychain
only gives it to a program whose signature the owner of the machine has allowed. `go install` gives
you a build signed for that build alone, so the keychain asks again after every update. Sign the
program with a certificate of your own and the answer survives:

```sh
make install SIGN_IDENTITY="crewflow (local)"    # the same go install, plus codesign
```

The name is the one your machine prints:

```sh
security find-identity -v -p codesigning
```

A certificate of your own is made in Keychain Access: **Certificate Assistant → Create a
Certificate**, *Create a new certificate*, type **Code Signing**, name it `crewflow (local)`, and
let it be created in the login keychain. `security find-identity -v -p codesigning` then lists it
under *Self Signed Certificates*, and that is the name to pass in `SIGN_IDENTITY`.

Without `SIGN_IDENTITY` the program is built and installed as it is, and `crewflow doctor` says
what that costs:

```
! binary signature   this program is signed for this build alone, and the next build is a
                    program macOS has not seen: CodeDirectory … flags=0x20002(adhoc,linker-signed)
```

When a window of the system does open, crewflow says what it is waiting for before it waits, and
stops waiting after two minutes: `blocked` with the reason `keychain-approval`, in the journal of
the attempt, in the report and in `-json`. A run refused the key before its executor started takes
the worktree and the branch it made away with it, so the next `task run` of the task simply starts
again. Nothing is stored outside the keychain.

## Use

Put a `crewflow.toml` in the root of your repository (see [the example](crewflow.toml)), then:

```sh
crewflow doctor            # tools, requirements, access, the identity of the executor and of the orchestrator
crewflow task check 12     # is issue #12 ready and, if risky, approved?
crewflow task run 12       # the executor works on it in its own worktree and opens a PR
crewflow review 14         # may PR #14 be merged, and if not, why not
crewflow review 14 -approve   # write REVIEW: APPROVED <full sha> on the head
crewflow merge 14         # fast-forward main to that sha, and prove where main is
crewflow verify 14        # main is the merged commit, the issue is closed, CI is green
crewflow task watch 12     # follow a run live
crewflow task list         # runs of this project, the ones that want you on top, and what else runs here
crewflow task list -all    # every run of every project on this machine
crewflow task check-stalled   # the runs that stand — for the orchestrator's schedule
crewflow task run 12 -continue "fix the failing test"   # the same session, after a review
```

Every key of `crewflow.toml` has a status in the code: `supported`, which the code
applies, or `specified`, which the design describes and the code has not written
yet. A value that asks for the second — `isolation.mode = "sandbox"`,
`merge.via = "forge"`, `parallel.max_tasks > 1`, a non-empty `capabilities` or
`fallback`, a `forge.kind` that is not `github` — is refused when the file is read,
and the refusal says what is not written, which issue writes it and what to write
instead. `crewflow doctor` shows the same list under `specified settings`, and in
`-json` under `specified`.

A run ends with one outcome: `pr-opened`, `blocked`, `blocked-secret`, `blocked-permission`,
`timeout`, `no-change-request`, `executor-failed`, `out-of-scope` or `interrupted` — and
`stalled` is not one of them, because such a run has not ended: see
[A run that stands](#a-run-that-stands). `blocked` is
the one a run that stopped by itself ends with, and it carries the reason — `keychain-approval`
when nobody answered the window of the keychain, the words of the executor otherwise.
`blocked-secret` is the one a run that reached for a key ends with: nothing continues such a task by itself, and the
report says which path it reached for and how. `task list` says which
project the tasks are counted on, who ran each of them and whose name it worked under
(`EXECUTOR`, `opencode · bot`), how its last try came out, how long ago that try began
(`AGE`), the attempt (`RUN`, `57-2`, and `—` where there was only one) and the change
request (`CHANGE`). It reads nothing but the local state, and nothing it prints changes what
the next run does: `-json` is the same answer it has always been.

## A run that stands

A run goes on working and gets quiet — a window of the keychain nobody answered, an executor
that hangs, a machine with no network. After `[executor] stall_after` (10 minutes by default)
crewflow calls it `stalled`, and the mark changes nothing: the process goes on, and the attempt
ends the way it would have ended.

```
43  feat(run): the list of runs      opencode · bot  stalled 11m  43-1  #45
```

`task list` shows the kind and the length of the silence, and `-json` holds `stalled_for` (in
seconds), `last_step` (the last line of the executor, read through its profile, or the last step
of crewflow) and `reason` (what the run is standing at, where crewflow knows it — for a run in
the mode of the `bot`, `keychain-approval` while the keychain window waits for a person). The
state of the task says `running` all the same: whether a run stands is worked out every time a
list is read, like an approval in §7h, and not stored as a flag that outlives the silence.

The journal of the attempt holds one line for the beginning of a silence and one for its end —
`crewflow: stalled — no activity for 11m, the last step: bash: go test ./...` — and never one
line a minute.

`crewflow task check-stalled` is made for a schedule: it says what stands, leaves **one record
under the task per episode** and one when the run is working again or is over, and exits
non-zero while anything stands. It reads the local state and touches the host only when there is
a record to write; a project whose host cannot write under a task is answered anyway, and the
answer says that no record was left.

The state of a run is written *before* crewflow asks the machine for anything, so a run standing
in front of the keychain window is in `~/.crewflow/state` while it stands there; a run refused
for another reason puts the state back as it was and leaves no attempt behind. See §7a of the
design.

## The gate

`crewflow review <PR>` gathers the facts of a change from the host and from git and answers
one question: may it be merged? When it may not, the answer names a single reason out of the
table of §7h — `approval-missing`, `approval-untrusted`, `approval-edited`, `approval-stale`,
`history-rewritten`, `owner-acceptance-missing`, `owner-acceptance-untrusted`,
`owner-acceptance-edited`, `pr-not-open`, `pr-draft`, `wrong-repository`,
`wrong-target-branch`, `out-of-scope`, `required-check-missing` / `-incomplete` / `-failed` /
`-untrusted`, `ci-sha-mismatch`, `not-fast-forward`, `forge-unavailable` — and says what to do
about it.

- an approval counts only from `[merge] reviewers` (in the mode `separate`, the account of the
  App of the orchestrator), only unedited, and only of the current head of the change. The gate
  counts a record by the account behind it — the kind of the account and the number the host keeps
  it under (`user.id` of a person, `performed_via_github_app.id` of an App) — and not by the string
  of the login, so a renamed App is still the same App and an App of another project under our
  login is not ours. Every login of `[merge] reviewers` and `[merge] owners` is asked of the host
  once, when the settings are read, and a login the host cannot name is an error that names the
  key of the list; the record of the App of the executor never counts, whatever the lists say
  (§7i);
- a record of the owner — the acceptance of a result (`ACCEPTED <full sha>`) and a file
  outside the boundaries of the task (`SCOPE: ACCEPTED <full sha> <why>`) — counts only from
  `[merge] owners` (the owner of the repository by default), which is one list and a
  different one from the reviewers: an orchestrator that works apart from the owner is not
  among the owners;
- the record of a review is a comment whose first line is `REVIEW: APPROVED <full sha>` or
  `REVIEW: CHANGES REQUESTED`; `-approve` and `-request-changes <file>` write it for you;
- a task marked `owner-check` (the name is `[acceptance] label`) is not merged until one of
  `[merge] owners` — the owner of the repository by default — writes `ACCEPTED <full sha>` of
  that exact head under the change; the report says on its own line that the result is
  waiting for him. A commit after the acceptance is a commit nobody has looked at, so the
  acceptance has to be written again;
- the required checks are the ones the rules of the branch demand, taken from GitHub Actions
  rather than from anybody's mark through the API of statuses;
- anything the host did not answer is `forge-unavailable`, never a pass.

See §7h of the design for the whole rule.

## The merge

`crewflow merge <PR>` asks the gate again, in the moment of the merge, and pushes
nothing unless it says the change may go in. What it pushes is one refspec of one commit —
`<full sha>:refs/heads/<default branch>`, no `--force`, given to git as arguments and
never as a line of a shell, because in a shell `$SHA:refs/…` is a modifier of a variable
and not a refspec.

The outcome is worked out from `git ls-remote`, not from the code `git push` exited with:

- `merged` — the branch of the host is at the approved commit; the task is waited for
  (the host closes it), the commit is recorded in the state of the task and the worktree
  of the task is taken away;
- `already-merged` — the branch already holds the head of the change, nothing was pushed;
- `push-rejected` / `not-fast-forward` — the host refused the push, or the branch moved
  on between the check and the push and the merge arrived too late;
- `verify-mismatch` — the push was accepted and the branch is somewhere else;
- a refusal of the gate — one reason of §7h and what to do about it, code ≠ 0, nothing
  pushed at all.

A merge that went through and a cleanup that did not is `merged-with-cleanup-warning`: the
merge stands, and the report says what is left to take away by hand. Every command of git
and everything it wrote is in `~/.crewflow/runs/<repo>/<N>-merge.jsonl`, next to the facts
the gate judged.

`crewflow verify <PR>` asks the three questions that close the cycle: is the branch of the
host at the commit that was merged, is the task closed, is the CI of that branch green. It
waits for the checks while they are going on, up to `[ci] timeout`, and records the moment
of the check in the state of the task.

## Releases

What changed in every version — added, changed, fixed, security — is in
[CHANGELOG.md](CHANGELOG.md), written in the language of the project. The format is
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), the versions are
[SemVer](https://semver.org/spec/v2.0.0.html), and the first release is `v0.1.0`, at the
closing of the `v1` milestone. Every task that changes what a person sees or can do writes
a line under `## [Не выпущено]` in its own pull request — rule 3 of
[PROJECT_RULES.md](PROJECT_RULES.md).

To cut a version:

1. In `CHANGELOG.md`, give the `## [Не выпущено]` section the number and the date of the
   release (`## [v0.1.0] - 2026-10-01`), put a fresh empty `## [Не выпущено]` above it, and
   merge that change as any other.
2. `scripts/release.sh v0.1.0` — it refuses, and says which part is missing, until that
   section is there with its date and the unreleased one is empty, the tree is clean and
   the branch is the default one. It builds `dist/crewflow` with the version, the commit
   and the moment stamped into it (`-ldflags -X …/buildinfo.Version`) instead of `dev`,
   prints what `crewflow version` answers, and puts an annotated tag on the commit.
3. Push and cut the release on the host — the script prints both commands and pushes
   nothing itself:

   ```sh
   git push origin main
   git push origin refs/tags/v0.1.0
   gh release create v0.1.0 --title "crewflow v0.1.0"
   ```

A build from a tag answers `crewflow version` with that version; a build from a worktree
answers `dev`, because a build from a worktree is no release.

## Executors

Any command-line agent that can run without a window. [OpenCode](https://opencode.ai) is tested
first:

```toml
[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "90m"           # how long a run may take: a hang is an outcome, not a wait
stall_after = "10m"       # how long it may be quiet before it is marked as standing (§7a)
```

## Whose name the executor works under

- **`owner`** (default): the login of the person (`gh auth`); `crewflow doctor` warns that the
  executor has the same powers.
- **`bot`**: a [GitHub App](https://docs.github.com/en/apps) with narrow rights, a token of an
  hour for one repository, commits by `crewflow-executor[bot]`, and a `pre-push` hook for the
  branch of the task. With a branch rule on `main` the App cannot bypass, the executor cannot push
  there even if a task talks it into it.

```toml
[identity]
mode = "bot"

[identity.github_app]
app_id = 5107052
installation_id = 0     # 0: found on the repository
```

```sh
crewflow auth app import ~/Downloads/<app>.pem   # into the macOS keychain; then delete the file
crewflow auth app check                          # key, installation, rights — never a token
```

The private key stays in the macOS keychain and never reaches the executor, a journal or a
terminal. See §7i of the design.

## Whose name the orchestrator works under

A review of a change and the push of a merge are the orchestrator's work, and the record of an
approval is only an approval because of the account it is written in. So the orchestrator has a
mode of its own, exactly as the executor:

- **`shared`** (default): the login of the person, together with the owner. `crewflow doctor`
  warns — one login cannot tell an approval of the orchestrator apart from an acceptance of the
  owner, and both are written by whoever runs crewflow.
- **`separate`**: a second [GitHub App](https://docs.github.com/en/apps) of the project,
  `crewflow-orchestrator`, with its own key in the keychain and its own token of an hour. Its
  rights are the rights of the executor plus Issues — write, because a merge closes the task
  behind the change that went in. `crewflow review` and `crewflow merge` then write and push as
  `crewflow-orchestrator[bot]`, and the gate counts its approval while counting nothing it writes
  as the owner: `[merge] owners` is a list of its own, and in this mode the App of the
  orchestrator is not in it.

Both projects crewflow works on — crewflow itself and telecli — are in this mode since 2026-10-01,
and `crewflow doctor` says so for both: `owner == orchestrator: NO` and an empty `trust_debt`.

```toml
[orchestrator]
mode = "separate"

[orchestrator.github_app]
app_id = 5107053
installation_id = 0     # 0: found on the repository

[merge]
reviewers = ["crewflow-orchestrator[bot]"]   # empty: the account of the App itself
owners = []                                  # empty: the owner of the repository
```

```sh
crewflow auth app import ~/Downloads/<orchestrator>.pem -as orchestrator   # a key of its own
crewflow doctor                                           # orchestrator: separate — GitHub App …
```

The branch rule on the default branch has to list the App of the orchestrator in its bypass list
— it is the account crewflow pushes the approved commit as. The App of the executor is never in
it. See §7i of the design.

The `separate` mode also refuses a file that would make the two subjects one: the owner of the
repository among `merge.reviewers`, or one login in both `merge.reviewers` and `merge.owners`, is
an error of the load and every command stops where it is. In the `shared` mode the same overlap is
not an error — one login is both subjects there — and `crewflow doctor` prints the debt of trust
instead of hiding it:

```
authority separation:
  owner:          naghuale
  orchestrator:   crewflow-orchestrator[bot] (separate)
  executor:       crewflow-executor[bot] (bot)
  owner == orchestrator: NO
  owners ∩ reviewers: none
```

Both lists are printed as the correspondence they are — the login of the file and under it the
kind and the number the gate counts that account by:

```
  reviewers:       crewflow-orchestrator[bot] (Bot 5140522)
  owners:          naghuale (User 93920024)
```

The same section is in `crewflow doctor -json` as `authority`, and the debts as `trust_debt` —
`owner-orchestrator-overlap` and `owners-reviewers-overlap`, a list and never a hole. A project of
the separate mode with both apps set up has an empty one.

The keychain asks the owner of the machine before it lets a program read a secret of it, and it
asks through a window of the system. crewflow says what it is waiting for before it asks — in the
terminal and in the journal of the run — and gives the wait an end: a run nobody answered is
`blocked: keychain-approval`, not a run that stands still. `crewflow doctor` answers the question
that can be answered without a window (`keychain access`) before any check of the report needs the
key, and prints each step as it makes it. Sign the program with `make install SIGN_IDENTITY=…` (see
[Install](#install)) and the "Always Allow" of that window survives the next build.

## Responsibilities

crewflow separates four roles:

- **executor** — writes the code of one task;
- **orchestrator** — reviews and coordinates;
- **owner** — approves risky work and accepts what people will see;
- **crewflow** — enforces the rules between them.

The executor may create changes, the orchestrator may approve them, the owner may accept them —
and none of them does the others' part. Who may do what, and what holds each rule, is the
responsibility matrix in §7k of the design.

## Fail-closed access

The executor works in three zones:

- 🟢 its worktree — read-write;
- 🟡 the dependencies the project names — read-only;
- 🔴 secrets (`~/.ssh`, the Keychain, cloud and GitHub credentials, `.env` files…) — never,
  whatever the project says.

A refused path simply stops the run; a refusal on a secret stops it as `blocked-secret`, and
crewflow never resumes such a run by itself (in progress). A false alarm is cheaper than a leaked
secret.

## A secret in a commit

A secret that reached a commit is not gone with the file it was in. The gate `secrets` of
`crewflow.toml` and the CI check of the same name stop the merge, and a person decides what a finding
is. `gitleaks` walks the commits of the change (`base..head`) on a pull request and the whole history
on `main`, and not every branch of the repository: a stub of a test in a branch that is not merged is
not a secret of this change. A finding that is not one of the placeholders below is a real secret: it
is not merged, it is revoked first and taken out of the history afterwards.

A placeholder of the project passes the check in one of two ways, both named in
[`.gitleaks.toml`](.gitleaks.toml):

- **by value** — the value itself stands in `regexes` of the first allowlist, with a word above it
  about where the value came from. This is the token of the example answer of the documentation of
  GitHub, the field `temp_clone_token` of that answer, and a commit a test asks for and does not
  find. A value is a value: the same placeholder in another file, in another branch or in another
  commit is still the same placeholder.
- **by a mark** — a value in the `testdata` of a package or in a `*_test.go` file, with the word
  `placeholder` on that same line. A test that needs a value of the shape of a secret says what it
  is, and the check reads the mark. Both conditions are needed: the path alone would let a real
  secret of a test through, and the mark alone would let one through anywhere.

A value in a test file without the mark is a finding, and so is a value of the shape of a secret in
any other file. So a test needs no change of the config: mark the line. A value that is not a value
of a test — a token of a run, a password — belongs in neither list, and the check is right to stop
the merge. The check proves both halves of that promise on every run of itself, on values it builds
in the runner and does not commit.

## Process health

crewflow measures the process, not the agents (in progress): first-pass success, clean after
merge, review rounds, time to merge, and the one biggest loss with the control that would reduce
it. Every number is computed from the facts of the process and never stored as state, and every
escaped defect has to leave a new test, gate check or acceptance rule behind (§7j, §8).

## What the executor may read

Only its worktree — read-write — plus the folders the project names **by commands**,
read-only:

```toml
[access]
read_from = [["go", "env", "GOMODCACHE"], ["go", "env", "GOROOT"]]
```

A task may ask for one folder more, in its own field, with the reason a person writes
next to it — for that run of that task and no other:

```markdown
### What to read outside the work folder and why

read: /opt/homebrew/include — the task builds the example against the C headers
```

Before the executor starts, the run is given a **map of its access**: where it may
write, what it may read outside the worktree with the hand that asked for it and the
reason, and what is never readable. The same map is the first thing `crewflow doctor`
prints and the first lines of the journal of the run, and a path without a reason is
refused by `crewflow task check` before anything starts.

The rights of a run come from crewflow alone. If the global OpenCode config of the
person holds a `permission` table, `doctor` says so and `crewflow task run` refuses to
start: OpenCode merges that file with the settings of the run, so those rights would
never have been written by crewflow.

Places of secrets (`~/.ssh`, `~/.gnupg`, `~/Library/Keychains`, `~/.config/gh`, `~/.aws`,
`~/.netrc`, `.env` files…) stay closed whatever a project or a task says. See §7d of the
design.

## License

[Apache-2.0](LICENSE). Some ideas come from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac),
see [NOTICE](NOTICE).
