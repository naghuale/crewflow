# Instructions for the executor

You are the **executor** in this repository. An orchestrator (another agent) writes the tasks as
GitHub issues, reviews your pull request and merges it. You implement exactly one task per run.

## Your run

1. You are already in a dedicated `git worktree` on a dedicated branch created for this task.
   Do not switch branches, do not create other worktrees, do not touch `main`.
2. Read the task: `gh issue view <N>` (the number is in your prompt). The issue is the source
   of truth: acceptance criteria, proof by tests, boundaries. Do exactly that — no more.
3. Work test-first: write the test that fails, then the code that makes it pass.
4. Before you open the pull request, all of these must pass locally:
   - `gofmt -l .` prints nothing
   - `go vet ./...`
   - `go test -race -count=1 ./...`
   - `golangci-lint run ./...` (if it is installed)
   - `go mod tidy -diff` prints nothing
5. Commit with a conventional message (`feat(scope): …`, `fix(scope): …`, `test(scope): …`),
   push the branch, and open a pull request with `gh pr create` using the template:
   the body starts with `Closes #<N>`, then what changed, how it was checked, risks.
6. Do not merge. Do not approve. Do not comment on your own pull request with `REVIEW:`.
7. When you are done, stop. Your final message is a short report: branch, commit, PR URL,
   and anything you could not do.

## If something is in the way

- If the task is ambiguous or contradicts the code, do not guess: stop and say so in your
  final message (`BLOCKED: <what and why>`). Do not open a pull request with a guess.
- If you need access outside the worktree, a system package, a secret or a network service
  that is not already available, do not work around it: stop with `BLOCKED: needs <what> — <why>`.
- Never invent an API from memory. Check the real source (`go doc`, the module source in the
  module cache via `go doc`, the official docs in the repo).

## Code conventions

- Go, standard library first; add a dependency only when the task says so.
- Every exported identifier has a doc comment. Comments explain *why*, not *what*.
- Errors are wrapped with context (`fmt.Errorf("load config: %w", err)`); sentinel errors for
  conditions a caller must tell apart; `errors.Is`, never string matching.
- No global state that tests cannot replace; inject clocks, environment, command runners.
- Tests never touch the real home directory, the real Keychain or the network: use
  `t.TempDir()`, fake runners and fake clocks.
