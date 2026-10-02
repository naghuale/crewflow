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

## The model

crewflow follows a model of development with AI agents, and the whole model is one sentence, quoted
here in the words of the model rather than translated:

> Всё, что можно доказать автоматически, должно выполняться автоматически. Всё, что нельзя доказать
> автоматически, остаётся решением человека.

[docs/MODEL.md](docs/MODEL.md) (in Russian) says what follows from it and why: the core principles
C1–C8, the roles, the levels of autonomy A0–A4, human decisions R1–R6, the levels of maturity
M0–M5 — and an honest table of what crewflow does with each of them today. The design is how the
model is built, the [project rules](PROJECT_RULES.md) are how we work now, and the code is what
actually works.

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
- ✅ a run that waited for you keeps its place: a checkpoint in the state of the task, the event
  of the request, and `crewflow task resume` to go on from it — you do the decision, not the run
- ✅ the attention queue: what of the project needs you right now, on top of `task list` and
  in full from `crewflow task attention` — a run that ended with an open PR, a run that
  stands, a change without a review, a result the owner has to accept, a run waiting for a
  decision of a person, and everything escalated after a day with one record per key
- ✅ one canonical answer for a program: `task attention -json`, `task list -json` and
  `task attention -all -json` write the same document with `version` and `generated_at` and two
  named lists — `runs` and `attention`, `attention_state` and `reason` are closed lists, and the
  block for a person is printed from the same records
- ✅ two identities for the orchestrator too, `shared` or a second GitHub App, and a gate that
  counts the records of the owner apart from the records of the orchestrator
- ✅ the merge gate in code: `crewflow review` says whether a change may be merged, and which
  one of the reasons of §7h says why it may not
- ✅ the owner's acceptance: a task marked `owner-check` is not merged until `ACCEPTED <sha of the
  head>` stands under its PR — the owner answers one word on the card the orchestrator shows
  (`принимаю` / `не принимаю`), and the orchestrator writes the record: the sha in the first line
  for the gate, the verbatim answer with its time and `recorded_by: orchestrator` beside it
- ✅ the merge itself: `crewflow merge` fast-forwards the branch to exactly the approved
  commit and proves it with `git ls-remote`; `crewflow verify` checks the branch, the
  issue and the CI of it afterwards

In progress:

- ⏳ `crewflow init` and recovery of a task that was interrupted mid-work (`task resume` covers
  a run that was stopped at a decision of yours; a run cut off in the middle of the work still
  needs `-continue`)

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

The commands that only **show** the state — `task attention` and `task list` — ask for no key at
all: what they count records by is in the answers of the host and in the file of the project, and
the host answers them as the login you run crewflow as. A key is read where crewflow acts in the
name of an App: a run, a review, a merge, and a record left under a task. `crewflow doctor` reads
keys on purpose — checking them is what it is for.

When nobody answered the window, the run leaves a **checkpoint** in the state of the task — the
step to go on from, the commit the branch stands at, the task as crewflow read it — and the event
of the request (`HUMAN_AUTHORIZATION_REQUIRED`) with the channel, the resource and the action. You
press "Always Allow", you run `crewflow task resume <N>`, and the run goes on in the same worktree
and the same session; you do not repeat the run. The continuation refuses and names its reason if
the head of the branch moved, if the task changed, if the checkpoint is older than the term of a
point (`[executor] resume_within`, a day), or if you refused the request — a refusal is not asked
again, and then `crewflow task run <N>` is the way. Nothing polls: every attempt to read the key
without the access opens a window of macOS of its own, so the key is read only when you name the
command, and even a refusal of the checkpoint does not open a window.

## Use

Put a `crewflow.toml` in the root of your repository (see [the example](crewflow.toml)), then:

```sh
crewflow doctor            # tools, requirements, access, the identity of the executor and of the orchestrator
crewflow task check 12     # is issue #12 ready and, if risky, approved?
crewflow task run 12       # the executor works on it in its own worktree and opens a PR
crewflow task admit 11 12  # write the record that lets two tasks run beside each other, and print K1–K8
crewflow task resume 12    # go on from the point a run stopped at, after a decision of yours
crewflow review 14         # may PR #14 be merged, and if not, why not
crewflow review 14 -approve   # write REVIEW: APPROVED <full sha> on the head
crewflow merge 14         # fast-forward main to that sha, and prove where main is
crewflow verify 14        # main is the merged commit, the issue is closed, CI is green
crewflow task watch 12     # follow a run live
crewflow task list         # runs of this project, the ones that want you on top, and what else runs here
crewflow task list -all    # every run of every project on this machine
crewflow task check-stalled   # the runs that stand — for the orchestrator's schedule
crewflow task attention     # what of this project needs you right now — same queue for a schedule
crewflow task attention -all  # what of every project on this machine needs you, out of the state alone
crewflow task run 12 -continue "fix the failing test"   # the same session, after a review
crewflow changelog build     # build the unreleased part of CHANGELOG.md out of changelog.d/
crewflow changelog check     # every fragment is a fragment of a task, and no two say one thing
crewflow doctor network      # what the route to the network can do, one capability at a time
```

Every key of `crewflow.toml` has a status in the code: `supported`, which the code
applies, or `specified`, which the design describes and the code has not written
yet. A value that asks for the second — `isolation.mode = "sandbox"`,
`merge.via = "forge"`, `parallel.max_tasks > 1`, a non-empty `capabilities` or
`[executor] fallback`, a `forge.kind` that is not `github` — is refused when the file is
read,
and the refusal says what is not written, which issue writes it and what to write
instead. `crewflow doctor` shows the same list under `specified settings`, and in
`-json` under `specified`.

A run ends with one outcome: `pr-opened`, `blocked`, `blocked-secret`, `blocked-permission`,
`timeout`, `no-change-request`, `executor-failed`, `out-of-scope` or `interrupted` — and
`stalled` is not one of them, because such a run has not ended: see
[A run that stands](#a-run-that-stands). `blocked` is
the one a run that stopped by itself ends with, and it carries the reason — `keychain-approval`
when nobody answered the window of the keychain, `provider-unavailable` or
`provider-error-unknown` when the model provider refused the run (see
[A provider that is not there](#a-provider-that-is-not-there)),
`network-route-unavailable` when neither route of the project reached the host and the work
of the task is kept (see [the route to the network](#the-route-to-the-network)), the words
of the executor otherwise.
`blocked-secret` is the one a run that reached for a key ends with: nothing continues such a task by itself, and the
report says which path it reached for and how. `task list` says which
project the tasks are counted on, who ran each of them and whose name it worked under
(`EXECUTOR`, `opencode · bot`), how its last try came out, how long ago that try began
(`AGE`), the attempt (`RUN`, `57-2`, and `—` where there was only one) and the change
request (`CHANGE`). It reads nothing but the local state, and nothing it prints changes what
the next run does.

## Two tasks beside each other

A second run of one project does not begin beside a going run without a **written record** of the
pair: `crewflow task run N` refuses it before it makes a worktree, and the refusal names the
command that writes the record.

```sh
crewflow task admit 11 12 -c K2=pass -c K3=pass -c K4=pass -c K5=pass -c K6=pass -c K7=pass -c K8=pass
```

```
admission of the pair #11, #12: allowed
  K1 write paths    pass — neither the declared boundaries nor the files of the working copies have anything in common
  K2 documents      pass
  …
  record ~/.crewflow/state/naghuale-crewflow/admission-11-12.json
```

**K1 the paths is worked out by crewflow**, out of the boundaries the two tasks declare and out of
the files their working copies have really changed; **K2…K8 the person enters**, with a reason
where the answer is not a `pass`, and a criterion nobody enters is `unknown` — not a pass. The
record goes to `~/.crewflow/state/<repo>/admission-<a>-<b>.json` and says what the eight criteria
are, the decision, and the world the decision was taken against.

At run time the record is applied fail-closed:

| What the record says | What the run gets |
|---|---|
| all eight `pass`, decision `allowed` | it goes |
| no record | `parallel-admission-required` |
| a record whose world has moved on — a changed specification, boundary, branch, set of running tasks, or a file both tasks may change | `parallel-admission-required`, with what changed |
| any criterion `unknown` | `parallel-admission-unknown criterion=K…` |
| any criterion `fail` | `parallel-admission-denied criterion=K… resource=…` |
| decision `owner-exception` (`-owner-exception "why"`) | it goes, and the journal of the run and the report say so — an experiment of the owner is not a safe pair |

Two runs going and a third starting is refused as well: a record of one pair admits no third task.
A run that goes on alone is not asked anything about pairs, and neither is a continuation of it —
`task run N -continue` and `task resume N` are held to exactly what a first run is held to, because
all three times the rule did not hold the action it was a continuation of a task (F-151). The words
above are a closed list, so a program reads one refusal for one reason.

A refusal spends nothing: it comes before the worktree, before the point of the task and before any
attempt, so no attempt, no journal and none of the automatic retries are spent on it.

An automatic continuation **inside** one run — the known habit, a stand that crewflow ends by itself,
a model provider that refused and may be asked again — starts a new *attempt* of that same run: the
same task, the same worktree, the same session, one `crewflow` process. It is not a second run of the
project, so there is no second pair and the record is not read a second time.

## A run that stands

A run goes on working and gets quiet — a window of the keychain nobody answered, an executor
that hangs, a machine with no network. After `[executor] stall_after` (10 minutes by default)
crewflow calls it `stalled`, and the mark by itself changes nothing: the process goes on, and
the attempt ends the way it would have ended.

```
43  feat(run): the list of runs      opencode · bot  stalled 11m  43-1  #45
```

**One exception, and it is the case of a hung agent.** A run that stands with no reason crewflow
knows — nobody is waiting for a window, the provider is answering — is a run whose agent has
hung, and crewflow stops it **once** and goes on in the same session and the same worktree, the
way it answers a known refusal (§7a.1 of the design). The journal of the hung attempt says so:
`crewflow: stalled — no activity for 12m …`, `crewflow: stopped the run here …` and
`crewflow: resumed once — the run showed nothing for 12m and was stopped there`; the next
attempt says `RUN_RESUMED` with the habit it went on for. On 2026-10-03 three runs hung like
that in a day and each time an orchestrator interrupted and continued the run by hand
(F-143, #37). **A run that hangs a second time is not stopped again**: it stands in the queue
where a person sees it, and that is the whole of what crewflow does about it. A run standing at
the keychain window is *waited for*, never cut — a person is at the machine there, not crewflow.
A project whose executor is quiet on purpose turns the continuation off with
`[executor] resume_stands = false`, and gets the mark alone.

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

## A provider that is not there

A model provider that does not answer is a **resource of the machine a run waits for**, not a
defect of the task: the branch, the worktree and the session stay where the run left them, a
checkpoint says where to go on from, and crewflow tries once more by itself. On 2026-10-02 both
nightly runs of #37 died with `Cannot connect to API (https://opencode.ai)` and
`isRetryable: true`, and both came out of `crewflow task run` as a plain `executor-failed` —
no reason, no retry, no work kept, and the task had to be started again by hand (F-119).

Only an **explicit mark** counts as a refusal of the provider: `isRetryable: true`, or a class
of failure the profile of the agent reads unambiguously. A run that failed for a reason of its
own and mentioned a provider in passing is not a run the provider refused, and a failure of the
provider that carries no mark is **not** a temporary unavailability — it is `provider-error-unknown`,
and none of it is repeated: a refused login, missing rights, a wrong request, a model that is not
set up, money or a quota are decisions of a person.

The retry is the policy of the project — `[executor] provider_retries` (one by default), counted
over the state of the task, so a task never retries itself behind a person's back. The second
refusal is **waits** (`blocked`, `provider-unavailable`), not "failed" and not "standing", and the
queue names four things: the provider, whether the provider said the refusal may be repeated,
that the work of the task is kept, and the command to go on — `crewflow task resume 43`, the same
session and the same checkpoint the run left (§7i). The journal of the attempt holds
`PROVIDER_UNAVAILABLE`, `PROVIDER_RETRY_SCHEDULED` and, after the run that went on, one
`PROVIDER_AVAILABLE` — the answer to the first call, not a promise about the next one.

```
ATTENTION REQUIRED
  #43 · blocked · provider-unavailable · the model provider of the run
    waiting 12m · next: orchestrator · crewflow task resume 43
    the work of the task is kept in the worktree and the session of the run, and the provider
    said the refusal may be repeated; crewflow goes on by itself once and no more
```

## The attention queue

`stalled` says that a run is quiet. The attention queue says what needs **you**: it is the answer
to the first question of an orchestrator and an owner, and it is the failure of the practice
journal it is there for — on 30.09 three runs finished with open PRs between 20:39 and 21:01 and
nobody noticed until 22:30, an hour and a half of "work is going on" (F-061, #37).

It is worked out every time it is read — from the state of the runs, and where a command may ask
the host, from the change requests, the review records and the labels of the tasks. It is never
stored next to the state: that would say what it said when it was written.

```
ATTENTION REQUIRED
  #41 · blocked · human-authorization-required · channel macos-keychain · the resource executor-key · the action human-execute
    waiting 2m · next: owner · crewflow task resume 41
  #43 · finished-unseen · run-completed · the change #113
    waiting 1h · next: orchestrator · crewflow review 113
```

`task list` shows the block over the table, and `crewflow task attention` is the whole queue with
the host of the project in it — a reviewed change is not in it at all. The two are asked the same
way and answer about the same tasks: `task list` asks the host about the tasks whose work may be
over and nothing else, so a table and a queue cannot call one and the same task different things.
Both count records by numbers — `performed_via_github_app.id` for an App, `user.id` for a
person, and the number of each App of the project is in `crewflow.toml` itself — and both ask
the host as the login you run crewflow as. **Neither reads a key of an App**: a queue that only
shows the state has no reason to ask the keychain of macOS anything, and on a build nobody has
trusted yet that question opens a window of the system and stands in front of it for two
minutes. On 2026-10-02 that is exactly what happened to the queue of #37, live: every task came
out as `read-failed` and the run took 120 seconds (`merge.reviewers[0]: … the private key of the
app 5140522 … nobody answered in 2m0s`, #130). A record *left under a task* is a different
matter: that is a write in the name of the App of the orchestrator, and it is made with the
roles of a review.
A run whose change is merged or closed, or whose task the host has closed, is remembered as settled
and is not asked about again for a day, so a machine that ran a hundred tasks reads a few.
`crewflow task attention` is made for a schedule: it exits non-zero while anything in the queue
wants a person, and leaves **one record under the task per key** (task, state, priority, reason),
not repeated for a day. The thresholds are in `[attention]`: `top_after` (30m),
`escalate_after` (24h), `remind_after` (24h), `weekly_after` (7d, the weekly slice of #37) and
`deadline_after` (30m) — the last one is the length of **one** wait: past it the queue stops
saying "nobody is asked for anything" and asks for a person instead.

**Waiting is only for something that can happen.** Three reasons, and none of them is "waiting
for CI": a change that conflicts with `main` is `conflict-with-main` and waits for the branch to
be rebased; a change whose head has no checks at all is `no-checks` and waits for somebody to
start them or to find out why they do not run; checks that are going are `ci-running`, and past
their `deadline_after` even that becomes `waited-too-long`. On 2026-10-02 PR #129 conflicted with
`main`, GitHub ran no CI for it, and "no checks" was read as "checks are going" for six hours
(F-110, #37). Where the CI of the project did not answer, or the host has not worked out yet
whether the change can be merged, the queue names **none** of the three and says it does not know:
the absence of an answer is not an answer about the checks. Green and red checks are the gate's
business (§7h); the queue only says whether anyone is waited for.

```
ATTENTION REQUIRED
  #43 · blocked · conflict-with-main · the merge into main of #129
    waiting 11m · next: orchestrator · crewflow task run 43 -continue "what is left to do" — rebase onto main and push
  #44 · awaits-resource · ci-running · the checks of the head of #130
    waiting 3m · next: nobody — we are waiting · the deadline 2026-10-03 12:31
```

A reason that is known is never "standing": a run at the keychain window *waits* for a person
(`blocked`, `human-authorization-required`, the channel and the action), and a run whose silence
has no reason at all *stands* — and standing is the one state that always wants a person, because
nothing is known to be broken and nothing is known to be going on.

The action it names is the command that goes on: press "Always Allow" in the window of the system
and run `crewflow task resume 41` — the run continues in its own worktree and session, and you do
not repeat anything but the decision. A run whose request you refused is named differently, with
`crewflow task run 41`: a refused point is not one to continue, and a queue that keeps naming a
command that always says no is a queue nobody reads.

A reaction takes a task out of the queue: a merged or closed pull request, a review of the head
the change stands at, the owner's `ACCEPTED`, a task the host has closed. What the host could not
be read is said in its own block, `COULD NOT READ`, and never as a task that wants a person: a
command stopped with Ctrl+C prints `interrupted` and no queue at all. Reading the queue takes more
than two seconds — it asks a service over a network — and says so in stderr while it reads. A run of a task the host
does not have at all — the repository moved and another one took the name — is a fact about the
project, not an expectation: it comes out as `task-missing`, is never escalated, never gets a
comment written under a task that is not there, and the row says where the run journal stayed.
And a host that could not be read is not the same thing: that entry is `unknown` and names the
problem, because crewflow does not know whether anybody has looked at the result.

## The answer for a program

`task attention -json` and `task list -json` write **one document**, so that a program reads the
state of the process in one format whichever command it asked — the queue, the list, and later
`status` and the statistics all build out of it, and none of them names a thing its own way. Two
named lists in it, and no nesting: `runs` are the runs, `attention` is what wants a person.

```json
{
  "version": 1,
  "generated_at": "2026-10-02T17:04:11+10:00",
  "repo": "naghuale/crewflow",
  "branch": "main",
  "runs": [ { "task": 43, "run": "43-1", "outcome": "stalled",
              "attention": { "attention_state": "stands", "reason": "no-progress", "…": "…" } } ],
  "attention": [ { "repo": "naghuale/crewflow", "task": 43,
                   "attention_state": "stands", "reason": "no-progress", "…": "…" } ]
}
```

`task list -json` fills both lists — the attention of a run is told in the run itself, under the
name `attention`, so a program reads a task either way. `task attention -json` fills `attention`
and leaves `runs` empty and named. `task list -all` and `task attention -all` put every project
of the machine into one such document, and every record names its own project.

`version` says which build of crewflow wrote the answer and grows only when the meaning of a
field changes; `generated_at` is the moment the answer was made, and every `waiting_since` and
`waiting_seconds` in it is counted to that moment.

`task attention -all` is the one answer about the machine and not about a project, and it says
so before its block: every project has its own tracker, its own host and its own file, so `-all`
reads the state of the runs and nothing else — no host is asked, and no record is left under a
task, because the record is written as the orchestrator of a project and the machine has none.
What that costs is in the same line: a change merged past crewflow stays `finished-unseen` in
that table until `crewflow task attention` in its own project asks its host and takes it out.

`attention_state` and `reason` are **closed lists** — the eight states and twenty-four reasons of
§6a of the design — because a program switches on those words. A reason crewflow cannot name
from the list is not written as a code: it stays in the `subject` of the record for a person to
read, and the code stays `blocked`, which is what it is. The lines above the table and the
record under the task are printed from the very records the document holds.

The format also **reserves** the fields that later tasks fill: `task_facts` (`resume`,
`checkpoint`, `repository_id`, `depends_on`, `priority`, `milestone`, `lock_mode`, `holder`) and,
in a record of the attention, `resource` with its `resource_type`, `channel` and `action`. A
field nobody has filled yet is not written at all, and the table of §6a says which task fills
which.

See §6a of the design.

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
- a task marked `owner-check` (the name is `[acceptance] label`) is not merged until
  `ACCEPTED <full sha>` of that exact head stands under the change; the owner answers one word on
  the card the orchestrator shows (`принимаю` / `не принимаю`) and the orchestrator writes the
  record, so the report says on its own line which commit the record is about and who wrote it. A
  commit after the acceptance is a commit nobody has looked at, so the acceptance has to be written
  again;
- the gate sees the author of that record and not the authenticity of the word in it: while the
  `gh` of the orchestrator works under the login of the owner (F-133), the two are one record to
  the host, so the protection of the decision is procedural and `recorded_by: orchestrator` names
  who wrote it — the mechanism that tells them apart is [#154](https://github.com/naghuale/crewflow/issues/154)
  (`later`), and the whole order is in §7f of the design;
- the required checks are the ones the rules of the branch demand, taken from GitHub Actions
  rather than from anybody's mark through the API of statuses;
- anything the host did not answer is `forge-unavailable`, never a pass.

See §7h of the design for the whole rule, and §7f
([«Решение владельца одним словом»](docs/DESIGN.md#решение-владельца-одним-словом)) for the order in
which the owner is asked and the record of the decision is written.

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
closing of the `v1` milestone.

A task does not write into `CHANGELOG.md`. Every task that changes what a person sees or
can do writes **its own fragment**, `changelog.d/<task number>.md` — the sections of the
journal and the lines for a person, with a link to the task and to the change — in its own
pull request, and rule 3 of [PROJECT_RULES.md](PROJECT_RULES.md) names that file as the
boundary of the task. A task touches no other fragment: it is out of the boundaries, and
the gate says `out-of-scope`.

`CHANGELOG.md` itself is built, never written by hand in an ordinary task:

```sh
crewflow changelog build     # the unreleased section out of every fragment
crewflow changelog check     # the gate: format, task exists, no duplicates
```

`build` writes the `## [Не выпущено]` section of `CHANGELOG.md` out of the fragments: by the
sections of the journal, and inside a section by the order the changes were merged — read out
of the history of the branch, the commit that added the fragment, and not from the number of
the task. Fragments that entered in the same second are read by the number, the larger one
first, so the journal is the same file on every machine. A build over a journal that already
is what the fragments build writes nothing at all.

`check` is what keeps the journal a journal: every fragment is a fragment, the number in its
name is a task that exists, and no two fragments say one thing. It does not read
`CHANGELOG.md` at all — a release writes that file, and a task that had to bring it to its
fragment would be a task writing the file every task writes. Every reason is named with the
file and the line in it, and `-json` answers the same reasons as fields for the orchestrator.
It is the only gate of a project that asks the host, and it belongs in `[[gates]]`:

```toml
[[gates]]
name = "changelog"
run = ["go", "run", "./cmd/crewflow", "changelog", "check"]
```

To cut a version:

1. `scripts/release.sh v0.1.0` — it refuses, and says which part is missing, until there is a
   fragment waiting in `changelog.d/`, the tree is clean and the branch is the default one. It
   then runs `crewflow changelog release v0.1.0`, which gives the unreleased section the number
   and the date of the release, leaves a fresh empty `## [Не выпущено]` above it and removes
   the fragments; the script commits that as one release commit and puts an annotated tag on
   it. It builds `dist/crewflow` with the version, the commit and the moment stamped into it
   (`-ldflags -X …/buildinfo.Version`) instead of `dev`, and prints what `crewflow version`
   answers. The host is not asked anything: a release does not wait for a task nobody can find.
   The release refuses what a version of the journal cannot hold twice — two fragments that
   say one thing, and a line a version below already holds — and it refuses before it writes,
   so a refused release has moved nothing and every version below stands byte for byte. The
   journal is replaced whole, by a rename: a release that was interrupted leaves either the
   journal of before or the one after, and never half of one of them.
2. Push and cut the release on the host — the script prints both commands and pushes
   nothing itself:

   ```sh
   git push origin main
   git push origin refs/tags/v0.1.0
   gh release create v0.1.0 --title "crewflow v0.1.0"
   ```

Between two releases `changelog.d/` may not be there at all: a project that has released
everything has no fragments, and that is not a fault of the check — it is nothing to build
from. The folder is back with the first fragment of the next task, and a release with nothing
waiting refuses itself.

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
resume_within = "24h"     # how long the checkpoint of a run waiting for your decision is good (§7i)
provider_retries = 1      # how many times crewflow goes on by itself after a refused provider (F-119, §7a.2)
resume_stands = true      # whether it goes on once itself after a run stood with a provider up (F-143, §7a)
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

### Traces of earlier App tokens in the keychain

A push of a run is signed by the helper of git of crewflow and by nothing else: the worktree of
a run — and the environment of the push of a merge — reset the list of helpers of git with an
empty value before crewflow's own helper is added to it, so `osxkeychain` of the system file of
macOS is not asked at all and no token of the App reaches it.

Before that reset git did ask it, and after a push went through it was handed the token of the
App: entries of `github.com` with the account `x-access-token` in the keychain, out of date an
hour later. Removing them is the owner's work, by hand and one entry at a time:
[docs/help/old-app-tokens.md](docs/help/old-app-tokens.md) says which entries are of crewflow
and what not to do. crewflow reads no keychain, writes none and deletes none.

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

**The only mode crewflow runs in is `host`, and `host` is not a sandbox of the operating
system.** The executor runs on your own machine, in a worktree of its own. Crewflow limits what a
run may do through the map of paths, the powers of its identity, the commands it checks and the
gates — it does not stop a process from reaching anything else on the host by means of the
operating system. "Isolation" in this mode means process and project separation, not containment
of the environment. The owner of the project set this boundary down after a review of the
documents of 02.10 found the word promising more than the mechanism gives
([#162](https://github.com/naghuale/crewflow/issues/162)).

What `host` **does** guarantee:

- a worktree and a branch of its own for the task, and your own checkout is never changed;
- a declarative map of paths: where to write, what to read outside the worktree, what is never
  readable — a place of a secret is closed whatever the project says;
- checks before the action: `crewflow task check` before the run, a refusal before the worktree is
  made, and a `pre-push` hook that takes the branch of the task and nothing else;
- credentials that are apart where the mode of a project asks for it: the executor under its own
  GitHub App (`identity.mode = "bot"`), the orchestrator under its own
  (`orchestrator.mode = "separate"`), the private key in the keychain of the machine —
  `crewflow doctor` says which of the two a run has;
- fail-closed: what crewflow cannot read or cannot name is refused, no right is granted by
  default, and the outcome of a run that reached for it is `blocked`, `blocked-secret` or
  `blocked-permission`.

What `host` **does not** guarantee:

- a container, a sandbox of the operating system, a namespace or a jail: there is none, and
  `isolation.mode = "sandbox"` or `= "container"` is refused when the file of the project is read;
- that a run cannot touch arbitrary paths of the host: the rights of a run are the permissions of
  the profile of the agent and the `CREWFLOW_READ` / `CREWFLOW_DENY` lists it is given, not a limit
  of the kernel, and a program that ignores the profile reads what the user who started it reads;
- network isolation: a proxy is put into the environment of the processes crewflow starts, and the
  traffic is limited by whatever limits the process of the owner;
- isolation of system APIs: the Keychain, `security`, sockets — everything the user who starts a
  run may do, the run may do too.

A separate environment is a decision of a person
([R1](docs/MODEL.md#решение-человека)): `sandbox` and `container` are described in §7d of the
design and not written yet.

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

## The route to the network

A proxy is a resource like any other: its address is different on every machine and
changes when you move, so `crewflow.toml` names the profiles and the commands change
them.

```sh
crewflow network proxy add home --type http --host 192.0.2.10 --port 1082
crewflow network proxy test home      # what that route can do, right now
crewflow network proxy use home       # the active profile
crewflow network mode proxy           # and go through it
crewflow network proxy list           # the profiles, and whether they take credentials
```

In the mode `proxy` the route is in the **environment of the processes crewflow starts** —
`gh`, `git`, the executor — and nowhere else: the environment of your shell, the global
git configuration and the system one stay as they are, so a proxy of one project does not
follow you into another checkout. In the mode `direct` nothing is added at all, because a
variable left over in a shell is a route nobody chose.

A target of `[network] no_proxy` goes straight out whatever the mode says — the rules of
the standard `NO_PROXY`: an exact name, a domain suffix, an address, a network in CIDR. A
rule that could be read two ways is refused rather than guessed at.

**A profile that is there is not a profile that works.** `network proxy test` and
`crewflow doctor network` check three capabilities separately — `github-api`,
`git-https`, `model-provider` — each with its own state (`available`, `unavailable`,
`unknown`, `interrupted`, `stale`), its own reason and the time the check took. Both routes
are checked and reported apart: what the straight route answered says nothing about the
profile, and what the profile answered says nothing about the straight route.

A reason is a *proven* explanation, never the most likely guess, and the list is closed:
`request-succeeded`, `proxy-authentication-required`, `dns-failed`, `connection-refused`,
`connection-timeout`, `tls-failed`, `authentication-failed`, `permission-denied`,
`endpoint-not-found`, `reference-not-found`, `proxy-usage-not-proven`, `check-unclassified`.
Nothing more specific than the evidence: a status code or an exit code first, a typed
error second, the class of the failure third, and `unknown` where none of them proves
anything. So a non-zero exit of git never counts as a working route on its own, and
credentials refused (`authentication-failed`, `permission-denied`) are `unknown` — the
network got there, the account did not, and that is not a reason to change the route.

The success of GitHub says nothing about the model provider: without a run that really went
through the route, that capability stays `unknown proxy-usage-not-proven`. Editing a profile
never activates it and never checks it: a profile can be prepared before it answers.

Credentials of a profile live in the keychain of the machine under
`network-proxy/<name>`, never in the file of the project — an address with a login in it
(`http://user:pass@…`) is refused:

```sh
crewflow network proxy edit work --credentials secret-store
crewflow network proxy credentials work set -file ~/.proxy-password
crewflow network proxy credentials work status   # credentials_configured: true, and nothing more
```

Every command that shows the route — `list`, `credentials status`, `test`, `doctor
network` — says *whether* the credentials are configured and never reads the value. It is
read in one place only: where a connection through the profile is really made. Removing a
profile from the file and removing its credentials from the keychain are two different
operations on purpose.

### The mode `fallback`: straight out, and one attempt through the profile

```sh
crewflow network mode fallback          # straight out first, one attempt through "home"
```

The third mode sends every request **straight out**, exactly as `direct` does, and makes
**one** more attempt through the active profile — and only then, when the first attempt was
refused by a *proven repeatable* failure of the network: `dns-failed`,
`connection-refused`, `connection-timeout`, `tls-failed`, `proxy-authentication-required`.
There is no second retry, and crewflow does not go on by itself afterwards.

The fallback happens **for one operation**: a GitHub API call that could not reach the
network is made once more through the profile, and the calls after it — the `git` of a run,
the executor, `doctor` — go out straight as they always did. A permanent change of the
route is a command of yours: `network proxy use`, `network mode proxy`. The second attempt
works under the same account and the same token as the first one: another road is not
another right.

It does **not** fire on a host that answered and refused: `401`, `403`, a wrong request, a
mistake in the settings or in the task, and anything crewflow cannot read are answers of
the host or of the task, and asking again through another route would only hide them behind
a longer wait.

When **neither** route reaches the host, the run ends as `blocked` with the reason
`network-route-unavailable`, the queue of attention says that the straight route and the
profile `<name>` are both unavailable with their own proven reason, and the work of the task
stays where the run left it — the worktree, the branch and the session, with the command
that goes on from them. The events of the route — `NETWORK_ROUTE_SELECTED`,
`NETWORK_ROUTE_FAILED`, `NETWORK_FALLBACK_STARTED`, `NETWORK_FALLBACK_SUCCEEDED`,
`NETWORK_FALLBACK_FAILED` — go into the journal of the run and never carry a credential.

See §7d of the design.

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
never have been written by crewflow. They are the rights of the profile of the agent, not a
sandbox of the operating system: what the mode `host` does and does not guarantee is in
[Fail-closed access](#fail-closed-access).

Places of secrets (`~/.ssh`, `~/.gnupg`, `~/Library/Keychains`, `~/.config/gh`, `~/.aws`,
`~/.netrc`, `.env` files…) stay closed whatever a project or a task says. See §7d of the
design.

## License

[Apache-2.0](LICENSE). Some ideas come from [zeroscrypt/aiac](https://github.com/zeroscrypt/aiac),
see [NOTICE](NOTICE).
