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
- ✅ the merge gate in code: `crewflow review` says whether a change may be merged, and which
  one of the reasons of §7h says why it may not
- ✅ the merge itself: `crewflow merge` fast-forwards the branch to exactly the approved
  commit and proves it with `git ls-remote`; `crewflow verify` checks the branch, the
  issue and the CI of it afterwards

In progress:

- ⏳ the owner's acceptance of a review build before a merge
- ⏳ `crewflow init` and recovery of an interrupted task

The design and the plan are in [docs/DESIGN.md](docs/DESIGN.md) (in Russian).

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
the attempt, in the report and in `-json`. Nothing is stored outside the keychain.

## Use

Put a `crewflow.toml` in the root of your repository (see [the example](crewflow.toml)), then:

```sh
crewflow doctor            # tools, requirements, access, the identity of the executor
crewflow task check 12     # is issue #12 ready and, if risky, approved?
crewflow task run 12       # the executor works on it in its own worktree and opens a PR
crewflow review 14         # may PR #14 be merged, and if not, why not
crewflow review 14 -approve   # write REVIEW: APPROVED <full sha> on the head
crewflow merge 14         # fast-forward main to that sha, and prove where main is
crewflow verify 14        # main is the merged commit, the issue is closed, CI is green
crewflow task watch 12     # follow a run live
crewflow task list         # runs of this project, the ones that want you on top, and what else runs here
crewflow task list -all    # every run of every project on this machine
crewflow task run 12 -continue "fix the failing test"   # the same session, after a review
```

A run ends with one outcome: `pr-opened`, `blocked`, `blocked-secret`, `blocked-permission`,
`timeout`, `no-change-request`, `executor-failed`, `out-of-scope` or `interrupted`. `blocked` is
the one a run that stopped by itself ends with, and it carries the reason — `keychain-approval`
when nobody answered the window of the keychain, the words of the executor otherwise.
`blocked-secret` is the one a run that reached for a key ends with: nothing continues such a task by itself, and the
report says which path it reached for and how. `task list` says which
project the tasks are counted on, who ran each of them and whose name it worked under
(`EXECUTOR`, `opencode · bot`), how its last try came out, how long ago that try began
(`AGE`), the attempt (`RUN`, `57-2`, and `—` where there was only one) and the change
request (`CHANGE`). It reads nothing but the local state, and nothing it prints changes what
the next run does: `-json` is the same answer it has always been.

## The gate

`crewflow review <PR>` gathers the facts of a change from the host and from git and answers
one question: may it be merged? When it may not, the answer names a single reason out of the
table of §7h — `approval-missing`, `approval-untrusted`, `approval-edited`, `approval-stale`,
`history-rewritten`, `pr-not-open`, `pr-draft`, `wrong-repository`, `wrong-target-branch`,
`out-of-scope`, `required-check-missing` / `-incomplete` / `-failed` / `-untrusted`,
`ci-sha-mismatch`, `not-fast-forward`, `forge-unavailable` — and says what to do about it.

- an approval counts only from `[merge] reviewers` (the owner of the repository by default),
  only unedited, and only of the current head of the change;
- the record of a review is a comment whose first line is `REVIEW: APPROVED <full sha>` or
  `REVIEW: CHANGES REQUESTED`; `-approve` and `-request-changes <file>` write it for you;
- the files the change touches are checked against the boundaries of its task, and only a
  reviewer may take a file outside them with `SCOPE: ACCEPTED <full sha> <why>`;
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

## Executors

Any command-line agent that can run without a window. [OpenCode](https://opencode.ai) is tested
first:

```toml
[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
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

## Process health

crewflow measures the process, not the agents (in progress): first-pass success, clean after
merge, review rounds, time to merge, and the one biggest loss with the control that would reduce
it. Every number is computed from the facts of the process and never stored as state, and every
escaped defect has to leave a new test, gate check or acceptance rule behind (§7j, §8).

## What the executor may read

Only its worktree, plus the folders the project names **by commands**, read-only:

```toml
[access]
read_from = [["go", "env", "GOMODCACHE"], ["go", "env", "GOROOT"]]
```

Places of secrets (`~/.ssh`, `~/.gnupg`, `~/Library/Keychains`, `~/.config/gh`, `~/.aws`,
`~/.netrc`, `.env` files…) stay closed whatever a project says. See §7d of the design.

## License

[Apache-2.0](LICENSE). Some ideas come from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac),
see [NOTICE](NOTICE).
