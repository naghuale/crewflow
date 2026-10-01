package run

import (
	"slices"
	"time"
)

// Format is the version of the canonical format of the state of the process — the one
// document `crewflow task attention -json` and `crewflow task list -json` answer in. It is
// a word of the format and not of the state of a task: `version` of a document says which
// build of crewflow wrote it, and a program that reads a version it does not know says so
// rather than reading a shape it has never seen (docs/DESIGN.md §6a, §7h).
//
// It changes when the meaning of a field changes, and not when a field is added: an
// orchestrator that reads a document of a newer version reads the fields it knows out of it
// and is not stopped by the ones it does not.
const Format = 1

// Document is the canonical answer of every command about the work of a project: the version
// of the format, the moment the answer was made, the project it is of, one record per run and
// the queue of attention over them. It is one document and not a list of documents, because
// состояние процесса имеет несколько представлений — очередь, список, статус, статистика —
// и каждое из них сегодня называет одно и то же по-своему (docs/DESIGN.md §6a).
//
// Every record of it is a record of §6a: `Entries` are the runs of the machine or of one
// project, `Attention` is the queue of what wants a person, and the two are read together
// because a program is asked about a task and not about a run (F-105, docs/DESIGN.md §6a).
type Document struct {
	// Version is the shape of the format, and it is [Format] in every document of this
	// build.
	Version int `json:"version"`
	// GeneratedAt is when the answer was made, and every waiting and every `waiting_since`
	// of it is counted to this moment: a document without it is a statement about the work
	// of a project with no way to tell how old it is (docs/DESIGN.md §6a).
	GeneratedAt time.Time `json:"generated_at"`
	// Repo is the project the document is of, as a person writes it, and it is nothing in
	// a document of the whole machine: у каждого проекта машины свой хостинг и свой файл
	// проекта, и записи о них лежат в одной таблице (docs/DESIGN.md §6).
	Repo string `json:"repo,omitempty"`
	// Branch is the branch the runs of the project are counted from, and it is nothing
	// where no project file was read: a list of the whole machine is asked from any folder
	// and a folder of no project says nothing about the branch of the work beside it.
	Branch string `json:"branch,omitempty"`
	// Total is how many tasks of the project were run and Left how many did not fit into
	// the list of them: a list of twenty entries and no number beside it is a list of the
	// whole project (docs/DESIGN.md §6).
	Total int `json:"total,omitempty"`
	Left  int `json:"left,omitempty"`
	// Entries are the records of the format: one per run, in the order a person reads the
	// list in. It is an empty list and never a null where there is no run to write, so that
	// a program may read it without asking whether the field is there (docs/DESIGN.md §6a).
	Entries []Entry `json:"entries"`
	// Attention is the queue of what of the work wants a person, with the runs of the host
	// that could not be read in it, and it is nothing in a document of the whole machine: a
	// queue is the answer of one project about one host (docs/DESIGN.md §6a).
	Attention *Queue `json:"attention,omitempty"`
	// Unreadable are the state files crewflow could not read, which say nothing about any
	// run. They are said once, in the root of the document, and go to stderr beside it as
	// well: a state file that could not be read is not an answer, and a document that
	// listed it twice would be read twice (docs/DESIGN.md §6a).
	Unreadable []string `json:"unreadable,omitempty"`
}

// Document is the canonical document of the queue of attention of one project: the entries
// of the queue, the runs crewflow could not read and the state files it could not read, all
// of them under the root that says which format this is and when it was made
// (docs/DESIGN.md §6a).
func (q Queue) Document(at time.Time) Document {
	queue := q.saidOnce()
	return Document{
		Version:     Format,
		GeneratedAt: at,
		Repo:        q.Repo,
		Entries:     []Entry{},
		Attention:   &queue,
		Unreadable:  q.Unreadable,
	}.canonical()
}

// Document is the canonical document of a list of runs: the records of the runs, the queue of
// attention over them where the list is of one project, and the state files crewflow could
// not read. A list of the whole machine is one document of every project of the machine, and
// every record of it says which project it is of (docs/DESIGN.md §6, §6a).
func (r Runs) Document(at time.Time) Document {
	document := Document{
		Version:     Format,
		GeneratedAt: at,
		Repo:        ownerAndRepo(r.Repo),
		Branch:      r.Branch,
		Total:       r.Total,
		Left:        r.Left,
		Entries:     r.Entries,
		Unreadable:  r.Unreadable,
	}
	// The queue belongs to one project and to one host: a list of the whole machine has no
	// queue of its own, and a document that put an empty one there would say that nothing
	// of any project wants a person, which is not what was read (docs/DESIGN.md §6a).
	if r.Repo != "" {
		queue := r.Attention.saidOnce()
		document.Attention = &queue
	}
	return document.canonical()
}

// saidOnce is the queue with the state files that could not be read taken out of it: they
// are said once, in the root of the document, and the queue keeps them for the block of a
// person and for the stderr beside the answer. A format that said the same list of paths in
// two places is a format nobody reads the same way twice (docs/DESIGN.md §6a).
func (q Queue) saidOnce() Queue {
	q.Unreadable = nil
	return q
}

// canonical is the document with the codes of §6a in it: a state or a reason out of the
// closed lists is not a code of the format and is not written into it, and everything else
// in the records stays as it was read. The words of a refusal are not lost by it — они
// остаются в объекте ожидания записи, который читает человек (§6a, §7e).
func (d Document) canonical() Document {
	if d.Entries != nil {
		// The records are copied before they are written into: a document is built out of
		// a list a caller keeps for the table it prints, и запись, вычищенная в документе,
		// не должна вычищать и её (docs.DESIGN.md §6a).
		d.Entries = slices.Clone(d.Entries)
		for at := range d.Entries {
			if one := d.Entries[at].Attention; one != nil {
				coded := one.coded()
				d.Entries[at].Attention = &coded
			}
		}
	}
	if d.Attention == nil {
		return d
	}
	queue := *d.Attention
	queue.Entries, queue.Unread = slices.Clone(queue.Entries), slices.Clone(queue.Unread)
	for at := range queue.Entries {
		queue.Entries[at] = queue.Entries[at].coded()
	}
	for at := range queue.Unread {
		if !KnownReason(queue.Unread[at].Reason) {
			// A read crewflow could not name the cause of is still a hole in the queue, and
			// the hole is written with a reason of §6a rather than with a word nobody
			// reads: неизвестная причина непрочитанного — это всё равно непрочитанное
			// (F-098, §6a).
			queue.Unread[at].Reason = ReasonReadFailed
		}
	}
	d.Attention = &queue
	return d
}

// coded is the entry with the codes of §6a in it: a state out of the closed list and a
// reason out of it are not written, and the rest of the entry stands as it was worked out.
// The reason of a refused run is a code of the queue even where the state of the task holds
// a word of its own — `refusalOf` says which code it is, and the word itself stays in the
// subject of the entry (docs/DESIGN.md §6a).
func (a Attention) coded() Attention {
	if !KnownState(a.State) {
		a.State = ""
	}
	if !KnownState(a.EscalatedFrom) {
		a.EscalatedFrom = ""
	}
	if !KnownReason(a.Reason) {
		a.Reason = ""
	}
	return a
}
