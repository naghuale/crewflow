package secret

import (
	"maps"
	"strings"
)

// The policies of the documents of §6a, §7e, §7h and §7i: what every field of every document
// crewflow publishes as JSON is, by the path it stands at. A field the policy of its document
// does not name is not published at all — the refusal is `output-redaction-path-unknown` and
// it carries the document and the path and nothing else (D-082, docs/DESIGN.md §7e).
//
// Three kinds of field, and the difference between them is whether the boundary may cut the
// value out of it: **text** is the words of a run, of a person or of a program, and every
// known form of a value of the run is taken out of it; **enum** is a word out of a closed list
// of the format and **identifier** is a name the format has a shape for, and the value of
// either is kept whole, because a program reads the document by these words and by these
// names, and a word of the format with a cut in it is not a word of the format (D-082, §6a,
// §7e). A number, a flag and a container are kept as they are: a number of a report that
// happens to be the password of a proxy is a number of the report (§7e, R5-NEW-4).
//
// The closed lists below are the boundary's copy of the lists of the format, and every one of
// them is held word by word against the constants of the package that owns it by a test in
// that package: a list of words is a statement about the format, and a copy of it that went
// from the code is a document published against a list nobody maintains (D-044, §6a).
//
// A row is one path: `field`, `field.child`, `field[]` for an element of a list and `field.*`
// for an element of a map whose keys the schema declares as dynamic. The names of the fields
// are not values and are kept whole: a document is read by its names, and a name with a cut
// in it is a document a program cannot read (R224-001, D-082).
var policies = map[Document]map[string]rule{
	DocumentTaskRun:          rooted(resultOfARun()),
	DocumentTaskResume:       rooted(resultOfARun()),
	DocumentTaskList:         rooted(documentOfTheProcess()),
	DocumentTaskAttention:    rooted(documentOfTheProcess()),
	DocumentTaskAttentionAll: rooted(documentOfTheProcess()),
	DocumentTaskCheck:        rooted(checkOfATask()),
	DocumentTaskAdmit:        rooted(merge(admissionOfAPair(""), map[string]rule{"path": ofForm(aPath)})),
	DocumentTaskCheckStalled: rooted(standingsOfAProject()),
	DocumentReview:           rooted(reviewOfAChange()),
	DocumentMerge:            rooted(mergeOfAChange()),
	DocumentVerify:           rooted(verifyOfAChange()),
	DocumentDoctor:           rooted(reportOfTheMachine()),
	DocumentDoctorNetwork:    rooted(checksOfTheRoutes()),
	DocumentNetworkProxyList: rooted(profilesOfAProject()),
	DocumentNetworkProxyTest: rooted(testOfARoute()),
	DocumentChangelogCheck:   rooted(checkOfTheJournal()),
	DocumentAuthAppCheck:     rooted(checkOfTheKeyOfAnApp()),
	DocumentState:            rooted(stateOfATask()),
}

// rooted is the table of a document with the rows its own shape demands: the row of the root —
// every document is an object or a list, and the row of the root says which — and the row of
// every list in it, because a list is a field of its own (`left`) and its elements are named
// under it (`left[]`). A policy that named the elements of a list and not the list would leave
// the document with a field nobody classified (D-082, docs/DESIGN.md §7e).
func rooted(rows map[string]rule) map[string]rule {
	if _, known := rows[""]; !known {
		rows[""] = aContainer()
	}
	for path := range rows {
		held, _, isList := strings.Cut(path, "[]")
		if !isList {
			continue
		}
		if _, known := rows[held]; !known {
			rows[held] = aContainer()
		}
	}
	return mapped(rows)
}

// mapsOf puts the row of every map of the table beside the rows of its elements: a map the
// schema declares as dynamic is a field of its own (`valid_while.specs`) and its elements are
// named under it (`valid_while.specs.*`).
func mapped(rows map[string]rule) map[string]rule {
	for path := range rows {
		elements, _, isMap := strings.Cut(path, ".*")
		if !isMap {
			continue
		}
		if _, known := rows[elements]; !known {
			rows[elements] = aContainer()
		}
	}
	return rows
}

// The closed lists of the format, in the order the packages that own them keep them.

// outcomesOfARun is the list of §7a: how a run ended, and the one value for a run that has
// not ended yet (`run.kinds`).
var outcomesOfARun = []string{
	"running", "stalled", "maybe-running", "interrupted", "timeout", "blocked-secret",
	"blocked-permission", "blocked", "executor-failed", "no-change-request", "pr-opened",
	"out-of-scope",
}

// statesOfAttention is the list of §6a: the eight states of the queue of attention
// (`run.States`).
var statesOfAttention = []string{
	"stands", "escalated", "finished-unseen", "awaits-review", "awaits-owner",
	"awaits-resource", "blocked", "stopped",
}

// reasonsOfAttention is the list of §6a: the reasons of the queue (`run.Reasons`).
var reasonsOfAttention = []string{
	"blocked", "blocked-permission", "blocked-secret", "change-closed", "change-merged",
	"checks-running", "conflict-with-main", "human-authorization-required", "no-checks",
	"no-progress", "out-of-scope", "owner-acceptance-required", "provider-error-unknown",
	"provider-unavailable", "read-failed", "read-interrupted", "resource-wait", "review-required",
	"run-completed", "run-ended", "run-failed", "run-timeout", "stopped-by-hand", "task-closed",
	"task-missing", "waited-too-long",
}

// reasonsOfTheGate is the table of §7h: why a change may not be merged (`gate.Reasons`).
var reasonsOfTheGate = []string{
	"approval-missing", "approval-untrusted", "approval-edited", "approval-stale",
	"history-rewritten", "owner-acceptance-missing", "owner-acceptance-untrusted",
	"owner-acceptance-edited", "pr-not-open", "pr-draft", "wrong-repository",
	"wrong-target-branch", "out-of-scope", "required-check-missing", "required-check-incomplete",
	"required-check-failed", "required-check-untrusted", "ci-sha-mismatch", "not-fast-forward",
	"push-rejected", "verify-mismatch", "forge-unavailable",
}

// resultsOfACriterion is the list of §7c: what one criterion of a pair came out as
// (`task.Results`), and decisionsOfAPair is the decision the eight of them add up to
// (`task.Decisions`).
var (
	resultsOfACriterion = []string{"pass", "fail", "unknown"}
	decisionsOfAPair    = []string{"allowed", "denied", "owner-exception"}
)

// statesOfACheck is the list of §7h: how the checks of a commit stand (`forge.CheckStates`),
// rulesOfABranch is what the host says about the rules of the branch (`forge.RuleStates`), and
// decisionsOfAReview is the two words of a record of a review (`gate.Decisions`).
var (
	statesOfACheck     = []string{"pending", "success", "failure", "none"}
	rulesOfABranch     = []string{"named", "unavailable-on-plan"}
	decisionsOfAReview = []string{"approved", "changes_requested"}
)

// outcomesOfAMerge is the list of §7h (`merge.Outcomes`), closedBy is the pair of words of who
// closed the task behind the change (`merge.ClosedBys`), and outcomesOfACheckpoint is what came
// of a request of a person (§7i, `run.WaitOutcomes`).
var (
	outcomesOfAMerge      = []string{"merged", "already-merged", "merged-with-cleanup-warning", "refused"}
	closedBy              = []string{"host", "crewflow"}
	outcomesOfACheckpoint = []string{"timeout", "completed", "denied"}
)

// statesOfACapability is the list of §7d: the five states a check of a route may be in
// (`network.CapabilityStates`), and statesOfACheckOfTheMachine is the answer of a check of
// `doctor` (`doctor.Statuses`).
var (
	statesOfACapability        = []string{"available", "unavailable", "unknown", "interrupted", "stale"}
	statesOfACheckOfTheMachine = []string{"ok", "warn", "fail"}
	capabilitiesOfARoute       = []string{"github-api", "git-https", "model-provider"}
)

// sourcesOfAccess is the list of §7d: the three who may ask for a folder (`access.Sources`).
var sourcesOfAccess = []string{"[access]", "task", "crewflow"}

// modesOfAnIdentity is the pair of words of §7i: the mode of an identity (`app.Modes`).
var modesOfAnIdentity = []string{"owner", "bot"}

// resultOfARun is the answer of `crewflow task run <N> -json` and of
// `crewflow task resume <N> -json`: the result of a run as §7i holds it. The record of a pair
// of tasks is in it under its own name, and its fields are classified once, in
// [admissionOfAPair], and put under both roots.
func resultOfARun() map[string]rule {
	return merge(
		admissionOfAPair("admission"),
		map[string]rule{
			"task":           aNumber(),
			"title":          freeText(),
			"outcome":        ofList(outcomesOfARun...),
			"branch":         ofForm(aName),
			"worktree":       ofForm(aPath),
			"profile":        ofForm(aName),
			"session":        ofForm(aName),
			"attempt":        aNumber(),
			"identity":       ofList(modesOfAnIdentity...),
			"continued":      aFlag(),
			"resumed":        aFlag(),
			"checkpoint":     aContainer(),
			"auto_resumed":   freeText(),
			"started_at":     ofForm(aMoment),
			"ended_at":       ofForm(aMoment),
			"journal":        ofForm(aPath),
			"error_journal":  ofForm(aPath),
			"exit_code":      aNumber(),
			"rejections[]":   freeText(),
			"reason":         ofWords(),
			"outside[]":      freeText(),
			"change_request": aContainer(),
			// The change request the run opened, as the host holds it. Its fields carry the
			// names of the host: the branch it goes into, the commit at its head, the state
			// of the change, the address where a person reads it, and the body of it — the
			// words of a person, published whole because the host keeps them.
			"change_request.BaseBranch": ofForm(aName),
			"change_request.Body":       ofForm(aBody),
			"change_request.Conflicted": aFlag(),
			"change_request.Draft":      aFlag(),
			"change_request.HeadBranch": ofForm(aName),
			"change_request.HeadSHA":    ofForm(aCommit),
			"change_request.MergeState": ofForm(aWord),
			"change_request.Number":     aNumber(),
			"change_request.Repository": ofForm(aRepository),
			"change_request.State":      ofForm(aWord),
			"change_request.URL":        ofForm(aURL),
			// The point of the task a run goes on from (§7i): where it stands, what came of
			// the request of a person, and the words of the step.
			"checkpoint.action":     freeText(),
			"checkpoint.assignment": ofForm(aFingerprint),
			"checkpoint.at":         ofForm(aMoment),
			"checkpoint.channel":    ofForm(aName),
			"checkpoint.decided_at": ofForm(aMoment),
			"checkpoint.head":       ofForm(aCommit),
			"checkpoint.outcome":    ofList(outcomesOfACheckpoint...),
			"checkpoint.resource":   freeText(),
			"checkpoint.step":       freeText(),
			"checkpoint.task":       aNumber(),
		})
}

// admissionOfAPair is the record of the pair of two tasks that may run beside each other
// (§7c), at the root of the answer of `crewflow task admit` and under `admission` in the answer
// of a run. The criteria are the eight of MODEL, IV-031: what a person entered for each of
// them, and what crewflow worked out for K1 — the globs and the files the two tasks have in
// common, which are the words of a person and of the tasks.
func admissionOfAPair(prefix string) map[string]rule {
	rows := map[string]rule{
		"":                     aContainer(),
		"version":              aNumber(),
		"repository_id":        ofForm(aName),
		"tasks":                aContainer(),
		"tasks.first":          aNumber(),
		"tasks.second":         aNumber(),
		"snapshot_id":          ofForm(aDigest),
		"criteria":             aContainer(),
		"decision":             ofList(decisionsOfAPair...),
		"created_at":           ofForm(aMoment),
		"valid_while":          aContainer(),
		"valid_while.active[]": aNumber(),
		// The boundaries of the two tasks are keyed by the number of the task, and their
		// globs and their files are what a person reads to see why the pair is or is not
		// admissible.
		"valid_while.boundaries.*":   aContainer(),
		"valid_while.boundaries.*[]": freeText(),
		"valid_while.branches.*":     ofForm(aName),
		"valid_while.specs.*":        ofForm(aPath),
		"valid_while.shared":         freeText(),
		"owner_exception":            aContainer(),
		"owner_exception.at":         ofForm(aMoment),
		"owner_exception.why":        freeText(),
	}
	for _, of := range []string{"K1", "K2", "K3", "K4", "K5", "K6", "K7", "K8"} {
		rows["criteria."+of] = aContainer()
		rows["criteria."+of+".result"] = ofList(resultsOfACriterion...)
		rows["criteria."+of+".reason"] = freeText()
		rows["criteria."+of+".resource"] = ofForm(aPath)
	}
	rows["criteria.K1.declared_patterns_overlap[]"] = freeText()
	rows["criteria.K1.concrete_files_overlap[]"] = freeText()
	return under(prefix, rows)
}

// documentOfTheProcess is the canonical document of §6a: the state of the process in the one
// format every command of it writes. `task list`, `task attention` and `task attention -all`
// publish the same document under three names, and its policy is one policy of three documents.
func documentOfTheProcess() map[string]rule {
	return merge(
		under("attention[]", attentionOfATask()),
		under("runs[]", runOfATask()),
		under("unread[]", unreadRun()),
		map[string]rule{
			"version":      aNumber(),
			"generated_at": ofForm(aMoment),
			"repo":         ofForm(aRepository),
			"branch":       ofForm(aName),
			"total":        aNumber(),
			"left":         aNumber(),
			"runs":         aContainer(),
			"attention":    aContainer(),
			"unread":       aContainer(),
			"unreadable[]": freeText(),
			"interrupted":  aFlag(),
		})
}

// attentionOfATask is one record of the queue of attention (§6a): what state the process of the
// task is in, the reason of it, who acts next, and the words a person reads about it. The
// state and the reason are words of the closed lists of §6a, and the rest is what a person and
// a program read: the object of the wait, the channel it is asked in and the action that ends
// it.
func attentionOfATask() map[string]rule {
	return map[string]rule{
		"":                aContainer(),
		"repo":            ofForm(aRepository),
		"task":            aNumber(),
		"title":           freeText(),
		"run":             ofForm(aName),
		"attention_state": ofList(statesOfAttention...),
		"escalated_from":  ofList(statesOfAttention...),
		"reason":          ofList(reasonsOfAttention...),
		"subject":         ofForm(aName),
		"channel":         ofForm(aName),
		"resource":        ofForm(aName),
		"action":          freeText(),
		"resource_type":   ofForm(aWord),
		"next_actor":      ofList("orchestrator", "owner", "nobody", "either"),
		"priority":        ofList("critical", "high", "normal"),
		"actable":         ofList("now", "watch", "wait", "unknown"),
		"waiting_since":   ofForm(aMoment),
		"waiting_seconds": aNumber(),
		"deadline":        ofForm(aMoment),
		"last_step":       freeText(),
		"next":            freeText(),
		"hint":            freeText(),
		"outcome":         ofList(outcomesOfARun...),
		"change":          aContainer(),
		"change.number":   aNumber(),
		"change.url":      ofForm(aURL),
		"long_waiting":    aFlag(),
		"promoted":        aFlag(),
		"said":            aFlag(),
		"unsaid":          freeText(),
		"problem":         freeText(),
		"refused":         freeText(),
	}
}

// runOfATask is one record of a run in the list of §6a, with the attention of the task under
// its own name: the words of the list are the outcome, the moment, the executor, the change
// request and what the task is known by.
func runOfATask() map[string]rule {
	return merge(
		under("attention", attentionOfATask()),
		map[string]rule{
			"":                 aContainer(),
			"repo":             ofForm(aRepository),
			"task":             aNumber(),
			"title":            freeText(),
			"attempts":         aNumber(),
			"run":              ofForm(aName),
			"outcome":          ofList(outcomesOfARun...),
			"started_at":       ofForm(aMoment),
			"ended_at":         ofForm(aMoment),
			"duration_seconds": aNumber(),
			"stalled_for":      aNumber(),
			"last_step":        freeText(),
			"reason":           freeText(),
			"executor":         ofForm(aName),
			"change":           aContainer(),
			"change.number":    aNumber(),
			"change.url":       ofForm(aURL),
			"identity":         ofList(modesOfAnIdentity...),
			"task_facts":       aContainer(),
			"attention":        aContainer(),
		})
}

// unreadRun is a run whose host could not be read (F-098, §6a): the reason is one of the two
// words of the format and the problem is what stood in the way.
func unreadRun() map[string]rule {
	return map[string]rule{
		"":        aContainer(),
		"task":    aNumber(),
		"run":     ofForm(aName),
		"title":   freeText(),
		"reason":  ofList("read-failed", "read-interrupted"),
		"problem": freeText(),
	}
}

// checkOfATask is the answer of `crewflow task check <N> -json`: what the task is missing
// before it may be run, one line per thing, in the words of a person who will write them.
func checkOfATask() map[string]rule {
	return map[string]rule{
		"task":      aNumber(),
		"title":     freeText(),
		"ready":     aFlag(),
		"missing[]": freeText(),
	}
}

// standingsOfAProject is the answer of `crewflow task check-stalled -json`: the runs that are
// standing, how long they have stood and what they last said.
func standingsOfAProject() map[string]rule {
	return map[string]rule{
		"[]":             aContainer(),
		"":               aContainer(),
		"[].task":        aNumber(),
		"[].title":       freeText(),
		"[].repo":        ofForm(aRepository),
		"[].run":         ofForm(aName),
		"[].outcome":     ofList(outcomesOfARun...),
		"[].stalled":     aFlag(),
		"[].stalled_for": aNumber(),
		"[].last_step":   freeText(),
		"[].reason":      ofWords(),
		"[].problem":     freeText(),
		"[].said":        aFlag(),
	}
}

// reviewOfAChange is the answer of `crewflow review <PR> -json` (§7h): the facts of the change
// and the answer of the gate about it. The words the gate answers by are of its closed lists,
// and the records of a review are what the host wrote — the author, the commit, the moment and
// the decision.
func reviewOfAChange() map[string]rule {
	return merge(
		under("review", recordOfAReview()),
		under("owner_accept", recordOfAnAcceptance()),
		map[string]rule{
			"repository":          ofForm(aRepository),
			"change":              aNumber(),
			"task":                aNumber(),
			"head":                ofForm(aCommit),
			"base":                ofForm(aName),
			"url":                 ofForm(aURL),
			"state":               ofForm(aWord),
			"merge_state":         ofForm(aWord),
			"conflicted":          aFlag(),
			"files":               aNumber(),
			"outside[]":           freeText(),
			"checks":              aContainer(),
			"checks[]":            aContainer(),
			"rules":               ofList(rulesOfABranch...),
			"owners[]":            ofForm(aName),
			"reviewers[]":         ofForm(aName),
			"acceptance_label":    ofForm(aName),
			"acceptance_required": aFlag(),
			"scope_accepted_by":   ofForm(aName),
			"verdict":             aContainer(),
			"verdict.ready":       aFlag(),
			"verdict.reason":      ofList(reasonsOfTheGate...),
			"verdict.detail":      freeText(),
			"checks[].name":       ofForm(aName),
			"checks[].sha":        ofForm(aSHA),
			"checks[].state":      ofList(statesOfACheck...),
			"checks[].want":       ofForm(aName),
			"checks[].app":        ofForm(aName),
			"checks[].missing":    aFlag(),
			"review":              aContainer(),
			"owner_accept":        aContainer(),
		})
}

// recordOfAReview is one record of a review as a report shows it (§7h): what was decided, about
// which commit, by whom, when, and whether it was edited since.
func recordOfAReview() map[string]rule {
	return map[string]rule{
		"":           aContainer(),
		"author":     ofForm(aName),
		"commit":     ofForm(aCommit),
		"decision":   ofList(decisionsOfAReview...),
		"created_at": ofForm(aMoment),
		"counted":    aFlag(),
		"edited":     aFlag(),
	}
}

// recordOfAnAcceptance is a record of an acceptance of a task of category R by its owner: the
// same shape as a record of a review and the same closed words.
func recordOfAnAcceptance() map[string]rule {
	return recordOfAReview()
}

// mergeOfAChange is the answer of `crewflow merge <PR> -json` (§7h, §7i): what came of the
// merge, whose name it was pushed under and what is left to do by hand.
func mergeOfAChange() map[string]rule {
	return map[string]rule{
		"task":                     aNumber(),
		"change":                   aNumber(),
		"url":                      ofForm(aURL),
		"head":                     ofForm(aCommit),
		"branch":                   ofForm(aName),
		"outcome":                  ofList(outcomesOfAMerge...),
		"verdict":                  aContainer(),
		"verdict.ready":            aFlag(),
		"verdict.reason":           ofList(reasonsOfTheGate...),
		"verdict.detail":           freeText(),
		"merged_sha":               ofForm(aCommit),
		"task_closed":              aFlag(),
		"task_closed_by":           ofList(closedBy...),
		"left[]":                   freeText(),
		"journal":                  ofForm(aPath),
		"orchestrator":             aContainer(),
		"orchestrator.mode":        ofList("shared", "separate"),
		"orchestrator.description": freeText(),
	}
}

// verifyOfAChange is the answer of `crewflow verify <PR> -json` (§7h): what is true about the
// change after the merge — where the branches stand, whether it was verified, and what is
// left of what was to be done around it.
func verifyOfAChange() map[string]rule {
	return map[string]rule{
		"task":        aNumber(),
		"change":      aNumber(),
		"url":         ofForm(aURL),
		"head":        ofForm(aCommit),
		"branch":      ofForm(aName),
		"main":        ofForm(aName),
		"merged":      ofForm(aName),
		"ci":          ofList(statesOfACheck...),
		"verified":    aFlag(),
		"verified_at": ofForm(aMoment),
		"task_state":  ofForm(aWord),
		"missing[]":   freeText(),
		"left[]":      freeText(),
		"note":        freeText(),
	}
}

// reportOfTheMachine is the answer of `crewflow doctor -json` (§7d, §7e): what the machine of
// the person is like — the folders a run may read and who asked for them, the rights of the
// project, the key of the App and who acts under it, the checks, and what is left to write.
func reportOfTheMachine() map[string]rule {
	return merge(
		under("access.read[]", grantOfAccess()),
		under("access.rejected[]", grantOfAccess()),
		under("authority.owners[]", subjectOfTheProject()),
		under("authority.reviewers[]", subjectOfTheProject()),
		under("authority.overlap[]", subjectOfTheProject()),
		map[string]rule{
			"access.deny":   aContainer(),
			"access.deny[]": freeText(),
			"trust_debt[]":  freeText(),
		},
		under("checks[]", checkOfTheMachine()),
		under("specified[]", askedOfAProject()),
		map[string]rule{
			"access":                          aContainer(),
			"access.read":                     aContainer(),
			"access.rejected":                 aContainer(),
			"authority":                       aContainer(),
			"authority.owner":                 ofForm(aName),
			"authority.owner_is_orchestrator": aFlag(),
			"authority.executor":              ofForm(aName),
			"authority.orchestrator":          ofForm(aName),
			"authority.owners":                aContainer(),
			"authority.reviewers":             aContainer(),
			"authority.overlap":               aContainer(),
			"identity":                        aContainer(),
			"identity.mode":                   ofList(modesOfAnIdentity...),
			"identity.account":                ofForm(aName),
			"identity.description":            freeText(),
			"orchestrator":                    aContainer(),
			"orchestrator.mode":               ofList("shared", "separate"),
			"orchestrator.account":            ofForm(aName),
			"orchestrator.description":        freeText(),
			"checks":                          aContainer(),
			"specified":                       aContainer(),
		})
}

// grantOfAccess is one folder of the table `[access]` of the project: the path, who asked for
// it, and why it is open in the words of whoever asked (§7d).
func grantOfAccess() map[string]rule {
	return map[string]rule{
		"":       aContainer(),
		"path":   ofForm(aPath),
		"source": ofList(sourcesOfAccess...),
		"reason": freeText(),
	}
}

// subjectOfTheProject is one subject of the project as the report of the separation of the
// three holds it: the account on the host, its number there and the kind of it (§7i).
func subjectOfTheProject() map[string]rule {
	return map[string]rule{
		"":      aContainer(),
		"id":    aNumber(),
		"kind":  ofForm(aWord),
		"login": ofForm(aName),
	}
}

// checkOfTheMachine is one check of the machine of the person: what was checked, how it came
// out and what to do about it.
func checkOfTheMachine() map[string]rule {
	return map[string]rule{
		"":       aContainer(),
		"name":   ofForm(aName),
		"status": ofList(statesOfACheckOfTheMachine...),
		"detail": freeText(),
		"hint":   freeText(),
	}
}

// askedOfAProject is one key of the file of the project that asks for a behaviour the design
// describes and the code does not have yet (§5).
func askedOfAProject() map[string]rule {
	return map[string]rule{
		"":        aContainer(),
		"key":     ofForm(aName),
		"value":   freeText(),
		"task":    aNumber(),
		"instead": freeText(),
	}
}

// checksOfTheRoutes is the answer of `crewflow doctor --network -json`: one check per ability
// of a route of the project (§7d).
func checksOfTheRoutes() map[string]rule {
	return map[string]rule{
		"[]":        aContainer(),
		"":          aContainer(),
		"[].name":   ofForm(aName),
		"[].status": ofList(statesOfACheckOfTheMachine...),
		"[].detail": freeText(),
		"[].hint":   freeText(),
	}
}

// profilesOfAProject is the answer of `crewflow network proxy list -json`: the profiles the
// project declares, with the kind of proxy each is and whether its credentials are configured
// — never the credentials themselves (§7d, §7e).
func profilesOfAProject() map[string]rule {
	return map[string]rule{
		"[]":                        aContainer(),
		"":                          aContainer(),
		"[].name":                   ofForm(aName),
		"[].type":                   ofList("http", "https", "socks5"),
		"[].host":                   ofForm(aName),
		"[].port":                   aNumber(),
		"[].active":                 aFlag(),
		"[].credentials":            ofList("none", "secret-store"),
		"[].credentials_configured": aFlag(),
	}
}

// testOfARoute is the answer of `crewflow network proxy test <name> -json`: the route a request
// goes out by and what came of checking every ability of it (§7d).
func testOfARoute() map[string]rule {
	return merge(
		under("capabilities[]", stateOfACapability()),
		map[string]rule{
			"route":          aContainer(),
			"route.direct":   aFlag(),
			"route.profile":  ofForm(aName),
			"route.fallback": ofForm(aName),
			"route.why":      freeText(),
			"capabilities":   aContainer(),
		})
}

// stateOfACapability is one ability of a route and how a check of it ended: the state is one of
// the five of §7d, the reason one of the closed words of the probe, and the detail the words
// of the check about it.
func stateOfACapability() map[string]rule {
	return map[string]rule{
		"":           aContainer(),
		"capability": ofList(capabilitiesOfARoute...),
		"result":     ofList(statesOfACapability...),
		"reason":     ofForm(aWord),
		"detail":     freeText(),
		"checked_at": ofForm(aMoment),
		"duration":   freeText(),
	}
}

// checkOfTheJournal is the answer of `crewflow changelog check -json`: the problems of the
// journal of the project, one by one — where it is, what is wrong with it and the name of the
// reason (§7h).
func checkOfTheJournal() map[string]rule {
	return map[string]rule{
		"problems":          aContainer(),
		"problems[]":        aContainer(),
		"problems[].path":   ofForm(aPath),
		"problems[].line":   aNumber(),
		"problems[].what":   freeText(),
		"problems[].reason": freeText(),
	}
}

// checkOfTheKeyOfAnApp is the answer of `crewflow auth app check -json`: whether the key of the
// App is in the store of the machine, and where the App is installed when crewflow could ask
// the host (§7e). The login of the account and its kind are what the host says about itself.
func checkOfTheKeyOfAnApp() map[string]rule {
	return map[string]rule{
		"mode":                       ofList(modesOfAnIdentity...),
		"description":                freeText(),
		"subject":                    ofList("executor", "orchestrator"),
		"app_id":                     aNumber(),
		"key":                        aFlag(),
		"installation":               aContainer(),
		"installation.id":            aNumber(),
		"installation.app_id":        aNumber(),
		"installation.account":       aContainer(),
		"installation.account.id":    aNumber(),
		"installation.account.login": ofForm(aName),
		"installation.account.type":  ofForm(aWord),
		"installation.permissions":   aContainer(),
		"installation.permissions.*": ofForm(aWord),
	}
}

// stateOfATask is the state of a task as it is kept in the file of it (§7h): a record of what
// crewflow did, written after the actions of a run. Its policy is complete — a test holds it
// against the fields of [run.State] — and a write of it never fails over a gap in it: the
// unknown field is cleaned as text and the gap is said as an event, because a record after an
// action is never lost for the sake of a policy (R224-16, D-082).
func stateOfATask() map[string]rule {
	return merge(
		under("attempts[]", attemptOfATask()),
		under("checkpoint", checkpointOfATask()),
		under("change", map[string]rule{"": aContainer()}),
		under("notice", map[string]rule{"": aContainer()}),
		under("settled", map[string]rule{"": aContainer()}),
		map[string]rule{
			"schema":                aNumber(),
			"task":                  aNumber(),
			"title":                 freeText(),
			"branch":                ofForm(aName),
			"worktree":              ofForm(aPath),
			"profile":               ofForm(aName),
			"session":               ofForm(aName),
			"change":                aContainer(),
			"change.number":         aNumber(),
			"change.url":            ofForm(aURL),
			"merged_sha":            ofForm(aCommit),
			"verified_at":           ofForm(aMoment),
			"notice":                aContainer(),
			"notice.key":            freeText(),
			"notice.at":             ofForm(aMoment),
			"settled":               aContainer(),
			"settled.by":            ofList(reasonsOfAttention...),
			"settled.at":            ofForm(aMoment),
			"checkpoint":            aContainer(),
			"checkpoint.action":     freeText(),
			"checkpoint.assignment": ofForm(aFingerprint),
			"checkpoint.at":         ofForm(aMoment),
			"checkpoint.channel":    ofForm(aName),
			"checkpoint.decided_at": ofForm(aMoment),
			"checkpoint.head":       ofForm(aCommit),
			"checkpoint.outcome":    ofList(outcomesOfACheckpoint...),
			"checkpoint.resource":   freeText(),
			"checkpoint.step":       freeText(),
			"checkpoint.task":       aNumber(),
			"attempts":              aContainer(),
			"attempts[]":            aContainer(),
		})
}

// attemptOfATask is one attempt of the executor in the state of a task: when it was started and
// stopped, what it did last, the identity it went under and the reason it ended by.
func attemptOfATask() map[string]rule {
	return map[string]rule{
		"":                   aContainer(),
		"number":             aNumber(),
		"outcome":            ofList(outcomesOfARun...),
		"journal":            ofForm(aPath),
		"error_journal":      ofForm(aPath),
		"reason":             ofWords(),
		"started_at":         ofForm(aMoment),
		"ended_at":           ofForm(aMoment),
		"reported_at":        ofForm(aMoment),
		"last_at":            ofForm(aMoment),
		"process_started_at": ofForm(aMoment),
		"pid":                aNumber(),
		"last_step":          freeText(),
		"executor":           ofForm(aName),
		"provider":           ofForm(aName),
		"identity":           ofList(modesOfAnIdentity...),
		"session":            ofForm(aName),
		"continued":          aFlag(),
		"resumed":            aFlag(),
		"auto_resumed":       freeText(),
	}
}

// checkpointOfATask is the point of the task a run goes on from (§7i).
func checkpointOfATask() map[string]rule {
	return map[string]rule{
		"":           aContainer(),
		"action":     freeText(),
		"assignment": ofForm(aSHA),
		"at":         ofForm(aMoment),
		"channel":    ofForm(aName),
		"decided_at": ofForm(aMoment),
		"head":       ofForm(aCommit),
		"outcome":    ofList(outcomesOfACheckpoint...),
		"resource":   freeText(),
		"step":       freeText(),
		"task":       aNumber(),
	}
}

// merge is the rows of the tables beside each other, in one table. A document is built of the
// records it carries, and each record is classified once, in its own function: the rows of a
// record are put under every root the document carries that record at, and the classification
// of a field is one row wherever the document shows it (D-044: one place).
func merge(tables ...map[string]rule) map[string]rule {
	rows := map[string]rule{}
	for _, table := range tables {
		maps.Copy(rows, table)
	}
	return rows
}

// under is the rows of a record with every path of them under one root: the record of the
// attention of a task stands at `attention[]` in the queue and under `attention` in the record
// of a run, and the classification of a field of it is one row, not two.
func under(root string, rows map[string]rule) map[string]rule {
	if root == "" {
		return rows
	}
	under := make(map[string]rule, len(rows))
	for path, what := range rows {
		if path == "" {
			// The record is an object: its own row says so, and its fields are under it.
			under[root] = what
			continue
		}
		under[root+"."+path] = what
	}
	return under
}
