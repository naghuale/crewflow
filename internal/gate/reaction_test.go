package gate

import (
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// Одобрение и приёмку изменения считает gate, и очередь внимания спрашивает у него
// то же, что ворота: своя копия этих двух правил в `cmd/crewflow` была вторым мнением о
// записи ревью, и она считала одобрение, которое ворота не считают (F-106, §6a, §7h).

// TestReactCountsTheRecordsTheWayTheGateDoes: то, что стоит под изменением, решается
// теми же функциями, что и вердикт ворот, — по числу учётной записи, по правке после
// публикации и по тому, чья это запись (docs.DESIGN.md §7h).
func TestReactCountsTheRecordsTheWayTheGateDoes(t *testing.T) {
	accepted := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	// reviewed is when [approvedBy] wrote the record: it is what a wait of a person is
	// counted from, and every case of this table that has a record that counts is
	// counted from it.
	reviewed := approvedBy(owner, second, false).CreatedAt
	for _, tc := range []struct {
		name       string
		facts      func() Facts
		approved   bool
		accepted   bool
		refusal    Reason
		wantLastAt time.Time
	}{
		{
			name:       "одобрение головы ревьюером",
			facts:      func() Facts { f := ready(); return f },
			approved:   true,
			accepted:   true,
			wantLastAt: reviewed,
		},
		{
			name: "последняя запись ревью — отредактированное одобрение",
			facts: func() Facts {
				f := ready()
				f.Reviews = []Review{
					approvedBy(owner, second, false),
					approvedBy(owner, second, true),
				}
				return f
			},
			approved:   false,
			accepted:   true,
			refusal:    ApprovalEdited,
			wantLastAt: reviewed,
		},
		{
			name: "запись не ревьюера — не одобрение",
			facts: func() Facts {
				f := ready()
				f.Reviews = []Review{approvedBy(executorApp, second, false)}
				return f
			},
			approved:   false,
			accepted:   true,
			refusal:    ApprovalUntrusted,
			wantLastAt: time.Time{},
		},
		{
			name: "одобрение есть, приёмки нет, а задача помечена",
			facts: func() Facts {
				f := ready()
				f.Labels = []string{ownerCheckLabel}
				f.OwnerAccepts = nil
				return f
			},
			approved:   true,
			accepted:   false,
			refusal:    OwnerAcceptanceMissing,
			wantLastAt: reviewed,
		},
		{
			name: "приёмка владельцем на голове",
			facts: func() Facts {
				f := ready()
				f.Labels = []string{ownerCheckLabel}
				f.OwnerAccepts = []OwnerAccept{
					{Author: owner, CreatedAt: accepted, Commit: second},
				}
				return f
			},
			approved:   true,
			accepted:   true,
			wantLastAt: accepted,
		},
		{
			name: "приёмка отредактирована после публикации",
			facts: func() Facts {
				f := ready()
				f.Labels = []string{ownerCheckLabel}
				f.OwnerAccepts = []OwnerAccept{
					{Author: owner, CreatedAt: accepted, Commit: second, Edited: true},
				}
				return f
			},
			approved:   true,
			accepted:   false,
			refusal:    OwnerAcceptanceEdited,
			wantLastAt: reviewed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reaction := React(tc.facts())

			if reaction.Approved != tc.approved {
				t.Errorf("одобрено = %v, want %v: очередь не должна считать одобрение иначе, чем ворота",
					reaction.Approved, tc.approved)
			}
			if reaction.Accepted != tc.accepted {
				t.Errorf("принято = %v, want %v", reaction.Accepted, tc.accepted)
			}
			if tc.refusal == "" {
				if reaction.Refusal.Reason != "" {
					t.Errorf("ворота отказали, want они не отказывали: %+v", reaction.Refusal)
				}
				return
			}
			if reaction.Refusal.Ready || reaction.Refusal.Reason != tc.refusal {
				t.Errorf("отказ = %+v, want %q: причина как у ворот", reaction.Refusal, tc.refusal)
			}
			if !reaction.Last.Equal(tc.wantLastAt) {
				t.Errorf("последняя запись, которая считается, = %s, want %s",
					reaction.Last, tc.wantLastAt)
			}
		})
	}
}

// TestReactIsReadOutOfTheFactsTheQueueHas: очередь не читает ни git, ни проверки CI, и
// её факты — это ровно то, что есть под изменением: голова, записи под ним и те, кому
// эти записи могут принадлежать. Одобрение более старого коммита — это `approval-stale`
// и не «история переписана»: ворота различают эти два по git, которого у очереди нет,
// и очередь не должна говорить о переписанной истории (docs/DESIGN.md §7h).
func TestReactIsReadOutOfTheFactsTheQueueHas(t *testing.T) {
	f := Facts{
		Head:      second,
		Reviews:   []Review{approvedBy(owner, first, false)},
		Reviewers: []forge.Subject{owner},
		Owners:    []forge.Subject{owner},
	}

	reaction := React(f)

	if reaction.Approved {
		t.Error("одобрение есть, want его нет: оно о коммите, которого головой уже не является")
	}
	if reaction.Refusal.Reason != ApprovalStale {
		t.Errorf("отказ = %q, want %q: очередь не читает историю и не говорит о переписанной",
			reaction.Refusal.Reason, ApprovalStale)
	}
	if reaction.Accepted != true {
		t.Error("принято = false, want true: задача не помечена, и приёмки никто не ждёт")
	}
}