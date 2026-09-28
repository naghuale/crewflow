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

## Executors

Any command-line agent that can run without a window works; the command is configured in
`crewflow.toml`. [OpenCode](https://opencode.ai) is tested first:

```toml
[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
```

## License

[Apache-2.0](LICENSE). Some ideas come from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac),
see [NOTICE](NOTICE).
