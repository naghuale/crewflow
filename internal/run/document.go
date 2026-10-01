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

// Document is the canonical answer of every command about the work of a project or of the
// machine: the version of the format, the moment the answer was made, the project it is of,
// and two named lists of records — the runs, and what of the work wants a person.
//
// It is one document and not a list of documents, because состояние процесса имеет несколько
// представлений — очередь, список, статус, статистика — и каждое из них сегодня называет
// одно и то же по-своему (docs/DESIGN.md §6a). The two lists are named and not nested: a
// word of the format means one thing and not two, so the record of a run and the record of
// what wants a person are told apart by their names and not by the place they sit in
// (F-105, docs/DESIGN.md §6a).
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
	// where no project file was read: a document of the whole machine is asked from any
	// folder and a folder of no project says nothing about the branch of the work beside it.
	Branch string `json:"branch,omitempty"`
	// Total is how many tasks of the project were run and Left how many did not fit into
	// the list of them: a list of twenty records and no number beside it is a list of the
	// whole project (docs/DESIGN.md §6).
	Total int `json:"total,omitempty"`
	Left  int `json:"left,omitempty"`
	// Runs are the records of the runs, in the order a person reads the list in. It is an
	// empty list and never a null where there is no run to write — an answer of the queue
	// alone says so by name and not by the absence of the field (docs/DESIGN.md §6a).
	Runs []Entry `json:"runs"`
	// Attention are the records of what wants a person, the most urgent first, and every
	// record names the project it is of. It is an empty list and never a null as well
	// (docs/DESIGN.md §6a).
	Attention []Attention `json:"attention"`
	// Unread are the runs whose host could not be read, and they are not records of the
	// attention: то, что не прочитано, не является утверждением о том, что кому-то нужно
	// внимание (F-098, docs/DESIGN.md §6a).
	Unread Unreads `json:"unread,omitempty"`
	// Unreadable are the state files crewflow could not read, which say nothing about any
	// run. They are said once, in the root of the document, and go to stderr beside it as
	// well (docs/DESIGN.md §6a).
	Unreadable []string `json:"unreadable,omitempty"`
	// Interrupted says that the read was cut short by a person and that what was read
	// before is not the whole of it (F-098, docs/DESIGN.md §6a).
	Interrupted bool `json:"interrupted,omitempty"`
}

// Document is the canonical document of the queue of attention of one project: the records
// of what wants a person, the runs of the host that could not be read and the state files
// that could not be read, all of them under the root that says which format this is and
// when it was made. The list of the runs is empty and named as such: очередь — это ответ о
// действии, и прогонов, которые не требуют его, она не перечисляет (docs/DESIGN.md §6a).
func (q Queue) Document(at time.Time) Document {
	return Document{
		Version:     Format,
		GeneratedAt: at,
		Repo:        q.Repo,
		Runs:        []Entry{},
		Attention:   q.Entries,
		Unread:      q.Unread,
		Unreadable:  q.Unreadable,
		Interrupted: q.Interrupted,
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
		Runs:        r.Entries,
		Unreadable:  r.Unreadable,
	}
	// The queue belongs to one project and to one host: a list of the whole machine has no
	// queue of its own, and a document that put an empty one there would say that nothing
	// of any project wants a person, which is not what was read (docs/DESIGN.md §6a).
	if queue, of := r.Attention[r.Repo]; of && r.Repo != "" {
		document.Attention = queue.Entries
	}
	if document.Attention == nil {
		document.Attention = []Attention{}
	}
	return document.canonical()
}

// canonical is the document with the codes of §6a in it: a state or a reason out of the
// closed lists is not a code of the format and is not written into it, and everything else
// in the records stays as it was read. The words of a refusal are not lost by it — они
// остаются в объекте ожидания записи, который читает человек (§6a, §7e).
func (d Document) canonical() Document {
	d.Runs = slices.Clone(d.Runs)
	for at := range d.Runs {
		if one := d.Runs[at].Attention; one != nil {
			coded := one.coded()
			d.Runs[at].Attention = &coded
		}
	}
	if d.Attention == nil {
		d.Attention = []Attention{}
	}
	d.Attention = slices.Clone(d.Attention)
	for at := range d.Attention {
		d.Attention[at] = d.Attention[at].coded()
	}
	d.Unread = slices.Clone(d.Unread)
	for at := range d.Unread {
		if !KnownReason(d.Unread[at].Reason) {
			// A read crewflow could not name the cause of is still a hole in the queue, and
			// the hole is written with a reason of §6a rather than with a word nobody
			// reads: неизвестная причина непрочитанного — это всё равно непрочитанное
			// (F-098, §6a).
			d.Unread[at].Reason = ReasonReadFailed
		}
	}
	return d
}

// coded is the record of one task with the codes of §6a in it: a state out of the closed list
// and a reason out of it are not written, and the rest of the record stands as it was worked
// out. The reason of a refused run is a code of the queue even where the state of the task
// holds a word of its own — `refusalOf` says which code it is, and the word itself stays in
// the subject of the record (docs/DESIGN.md §6a).
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
