# crewflow

**A fail-closed coordination protocol for coding agents: one task, one worktree, one reviewed SHA,
one verified merge.**

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
- git hosting stays the source of truth.

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

In progress:

- ⏳ the merge itself and the check after it (`crewflow merge`, `crewflow verify`)
- ⏳ the owner's acceptance of a review build before a merge
- ⏳ `crewflow init` and recovery of an interrupted task

The design and the plan are in [docs/DESIGN.md](docs/DESIGN.md) (in Russian).

## Install

Requires Go 1.27 or later and the [GitHub CLI](https://cli.github.com/) (`gh`), logged in.

```sh
go install github.com/naghuale/crewflow/cmd/crewflow@latest
```

## Use

Put a `crewflow.toml` in the root of your repository (see [the example](crewflow.toml)), then:

```sh
crewflow doctor            # tools, requirements, access, the identity of the executor
crewflow task check 12     # is issue #12 ready and, if risky, approved?
crewflow task run 12       # the executor works on it in its own worktree and opens a PR
crewflow review 14         # may PR #14 be merged, and if not, why not
crewflow review 14 -approve   # write REVIEW: APPROVED <full sha> on the head
crewflow task watch 12     # follow a run live
crewflow task list         # runs of this project, the ones that want you on top, and what else runs here
crewflow task list -all    # every run of every project on this machine
crewflow task run 12 -continue "fix the failing test"   # the same session, after a review
```

A run ends with one outcome: `pr-opened`, `blocked`, `blocked-permission`, `timeout`,
`no-change-request`, `executor-failed`, `out-of-scope` or `interrupted`. `task list` shows
the agent (`EXECUTOR`), whose name it worked under (`AS`: `owner` or `bot`), the attempt
(`RUN`, `57-2`) and the change request (`CHANGE`), and reads nothing but the local state.

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
