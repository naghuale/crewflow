# crewflow

A small, project-agnostic framework for developing with two roles: an orchestrator that plans,
reviews and merges, and an executor that writes code for one task at a time. Both roles are
played by whatever agents you configure, with whatever models they are set up to use: crewflow
does not depend on a particular agent, model, language or stack. The executor is launched headless
in its own `git worktree` for each task. GitHub issues and pull requests are the source of truth;
merges are fast-forward only, of the approved commit, on green CI.

Status: design draft — see [docs/DESIGN.md](docs/DESIGN.md).

Ideas borrowed with thanks from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac) (Apache-2.0).
