# crewflow

A small, project-agnostic framework for developing with two roles: an **orchestrator** that
plans, reviews and merges, and an **executor** that writes code for one task at a time. Both
roles are played by whatever agents you configure, with whatever models they are set up to use:
crewflow does not depend on a particular agent, model, language or stack.

- The executor is launched **headless** in its own `git worktree` for each task.
- A task is an issue written as a **readable spec** for a human, with a collapsed technical part
  for the executor.
- Issues and pull requests are the source of truth. A review is a comment
  `REVIEW: APPROVED <sha>`; a merge is **fast-forward only**, of exactly the approved commit, on
  green CI.
- Project gates (format, lint, tests, build…) and requirements are plain commands in
  `crewflow.toml`.

Status: early. The first milestone works and is used daily on a real project; the design and
the plan are in [docs/DESIGN.md](docs/DESIGN.md) (in Russian).

## Install

Requires Go 1.27 or later and the [GitHub CLI](https://cli.github.com/) (`gh`), logged in.

```sh
go install github.com/naghuale/crewflow/cmd/crewflow@latest
```

## Use

Put a `crewflow.toml` in the root of your repository (see [the example](crewflow.toml) and
§5 of the design), then:

```sh
crewflow doctor            # check tools, requirements and access
crewflow task check 12     # is issue #12 ready to be worked on?
crewflow task run 12       # run the executor on it in its own worktree; opens a PR
crewflow task watch 12     # follow a run live
crewflow task list         # what has been run here, and what is going on right now
crewflow task run 12 -continue "fix the failing test"   # continue the same session
```

A run ends with one outcome: `pr-opened`, `blocked`, `blocked-permission`, `timeout`,
`no-change-request`, `executor-failed`, `out-of-scope` or `interrupted`. Journals live in
`~/.crewflow/runs`, worktrees in `~/.crewflow/worktrees`.

`crewflow task list` shows the runs of the project from those journals and the state in
`~/.crewflow/state`: the ones that are going on top, the rest from the last to the first,
with the outcome of the last try, how long it took and the change request it opened. A run
whose state says "running" while its process is gone (a closed window, a rebooted machine) is
shown as `interrupted`. `-all` shows every run instead of the last twenty, and `-json` is the
same list for an orchestrator.

## Whose name the executor works under

crewflow has two modes, and it says in every report which one a run went under
(`executor: bot — GitHub App crewflow-executor (installation 12345)` or
`executor: owner — the login gh naghuale (shared rights)`): in `crewflow doctor` and in
its JSON, at the top of `task run` and `task watch`, in a column of `task list`, as the
first line of a run's journal and in the state of the task, so that every attempt is
attributable.

- **`owner` (the default)** is what crewflow has always done: the executor works under the
  login of the person (`gh auth`), nothing has to be set up, and `crewflow doctor` warns
  that the powers of the executor are the powers of that login.
- **`bot`** is the separated one: the executor works as a [GitHub
  App](https://docs.github.com/en/apps) with narrow rights (contents and pull requests
  write, issues and metadata read), a token of an hour for this repository only, commits
  made by `crewflow-executor[bot]`, and a `pre-push` hook that lets nothing but the
  branch of the task through. Together with a branch rule on `main` that the App cannot
  bypass, the executor cannot push there even if the task talks it into it.

```toml
[identity]
mode = "bot"

[identity.github_app]
app_id = 5107052        # the number of the app in the settings of GitHub
installation_id = 0     # 0: crewflow finds the installation on the repository
```

```sh
crewflow auth app import ~/Downloads/crewflow-app.pem   # into the macOS keychain, then delete the file
crewflow auth app check                                  # key, installation, rights — never a token
```

The private key of the App lives in the macOS keychain, is read only to sign a token, and
never reaches the executor, a journal, an error or a terminal. The token itself is in the
environment of the executor (which is why it is short, for one repository and with narrow
rights) and is taken out of everything crewflow writes. Bot modes for other hosts and a
Linux secret store are M7/M8; on Linux the mode `bot` for GitHub says it is not supported.
See §7i of the design.

## Executors

Any command-line agent that can run without a window works; the command is configured in
`crewflow.toml`. [OpenCode](https://opencode.ai) is tested first:

```toml
[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
```

## What the executor may read

The executor works in its worktree and is refused a permission for anything outside it — including
the sources of the libraries it depends on. A project says in `[access]` **how to find** them, with
commands rather than paths, because the paths are different on every machine:

```toml
[access]
read_from = [["go", "env", "GOMODCACHE"], ["go", "env", "GOROOT"]]
read = ["/opt/homebrew/include"]
```

crewflow runs those commands before the run and opens the folders it is given to **read only**, in
both spellings of every path: `/var` and `/private/var` are one folder in two names, and an agent
asks in the one it wrote. The places that hold secrets — `~/.ssh`, `~/.gnupg`,
`~/Library/Keychains`, `~/.config/gh`, `~/.aws`, `~/.netrc`, `~/.docker/config.json`, `~/.kube`,
`.env` files — stay closed whatever a project says, to reading and to writing, as does the `.env`
of the worktree itself; so do the root of a disk, the home folder and anything a secret sits in.
`crewflow doctor` shows what is open on this machine and what was refused and why; the first line
of the journal of a run is the policy it was started with. See §7d of the design.

## License

[Apache-2.0](LICENSE). Some ideas come from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac),
see [NOTICE](NOTICE).
