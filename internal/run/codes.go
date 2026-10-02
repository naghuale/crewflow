package run

import (
	"slices"
)

// The codes of the format of §6a are closed lists: `attention_state` is a word out of
// [States] and `reason` is a word out of [Reasons], and a word out of them is not a code of
// the format. A program that reads `-json` switches on these words and nothing else, so a
// value the format does not name is not written into it: очередь, очередь которая называет
// причиной то, чего нет в перечне, заставляет каждую читающую её команду считать свой
// список (docs/DESIGN.md §6a, §7e).
//
// The lists are the constants of `attention.go` and nothing else: a code added to the queue
// and forgotten in a list is a code no reader of the format knows, and the test of §6a
// holds both lists against the constants word by word.

// States is the closed list of the states of the queue of attention, in the order of §6a:
// stands, escalated, awaits-owner, awaits-review, awaits-resource, finished-unseen, blocked,
// stopped. The order is the order a person reads the queue in (AQ-005), and it is the one
// the queue sorts by (docs/DESIGN.md §6a).
//
// It is a function and not a variable, so that no caller of it can take a word out of the
// list of the format for everybody who reads it after.
func States() []AttentionState {
	return []AttentionState{
		AttentionStands,
		AttentionEscalated,
		AttentionAwaitsOwner,
		AttentionAwaitsReview,
		AttentionAwaitsResource,
		AttentionFinishedUnseen,
		AttentionBlocked,
		AttentionStopped,
	}
}

// reasons is the closed list of §6a, in one order so that a person reads it as a table and
// finds a word in it. It is held once and given out as a copy: a list of codes is a
// statement about the format, and no caller of it may edit that statement.
var reasons = []string{
	ReasonBlocked,
	ReasonBlockedPermission,
	ReasonBlockedSecret,
	ReasonChangeClosed,
	ReasonChangeMerged,
	ReasonChecksRunning,
	ReasonConflictWithMain,
	ReasonHumanAuthorization,
	ReasonNoChecks,
	ReasonNoProgress,
	ReasonOutOfScope,
	ReasonOwnerAcceptance,
	ReasonReadFailed,
	ReasonReadInterrupted,
	ReasonResourceWait,
	ReasonReviewRequired,
	ReasonRunCompleted,
	ReasonRunEnded,
	ReasonRunFailed,
	ReasonRunTimeout,
	ReasonStoppedByHand,
	ReasonTaskClosed,
	ReasonTaskMissing,
	ReasonWaitedTooLong,
}

// Reasons is the closed list of the reasons of the queue of attention — of a task in the
// queue and of a run crewflow could not read, in one block or the other (docs/DESIGN.md §6a).
func Reasons() []string {
	return slices.Clone(reasons)
}

// KnownState is whether the word is a state of the closed list of §6a. A state that is not
// one has no place in the queue and is not written into the document: нет девятого слова,
// которое читатель перечисления ждёт (docs/DESIGN.md §6a).
func KnownState(state AttentionState) bool {
	return slices.Contains(States(), state)
}

// KnownReason is whether the name is a reason of the closed list of §6a. A name that is not
// one is kept where it was said — in объекте ожидания записи, который читает человек, — and
// is not a reason of the format (docs/DESIGN.md §6a, §7e).
func KnownReason(reason string) bool {
	return slices.Contains(reasons, reason)
}
