# crewflow

A small, project-agnostic framework for developing with two agents: an orchestrator (Claude Code)
that plans, reviews and merges, and an executor (OpenCode by default) launched headless in its own
`git worktree` for each task. GitHub issues and pull requests are the source of truth; merges are
fast-forward only, of the approved commit, on green CI.

Status: design draft — see [docs/DESIGN.md](docs/DESIGN.md).

Ideas borrowed with thanks from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac) (Apache-2.0).
