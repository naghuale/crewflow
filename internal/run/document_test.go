package run

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The canonical document of the state of the process: the version of the format, the moment
// it was made, and one record per run with the queue of attention over them. Every command
// that answers about the work of a project answers in it, so that a program has one format
// to read and one place to look for a field of it (docs/DESIGN.md §6a).

// TestTheDocumentSaysWhichFormatItIsAndWhenItWasMade: the two fields that let a program
// read the answer of a build of crewflow it does not know. `version` is the shape of the
// format and `generated_at` is the moment every waiting and every `waiting_since` of the
// document is counted to: a document without them is a fact about the work of a project
// with no way to tell when it was true (docs/DESIGN.md §6a).
func TestTheDocumentSaysWhichFormatItIsAndWhenItWasMade(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	at := monday.Add(10 * time.Hour)
	writeState(t, home, repo, 43, "открыл изменение", &Change{Number: 44},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened})
	queue := queueOf(t, home, repo, attentionEnvOf(at), nil)

	document := queue.Document(at)

	if document.Version != Format {
		t.Errorf("the version of the format = %d, want %d", document.Version, Format)
	}
	if !document.GeneratedAt.Equal(at) {
		t.Errorf("the document was made at %s, want %s", document.GeneratedAt, at)
	}
	// The answer is one document and not a list of them: the queue of a project comes
	// under the same root as the list of its runs, and both name the project they are of.
	said := answerAs(t, document)
	for _, want := range []string{
		`"version": 1`,
		`"generated_at": "` + at.Format(time.RFC3339) + `"`,
		`"repo": "naghuale/crewflow"`,
		`"runs": []`,
		`"attention": [`,
		`"attention_state": "finished-unseen"`,
		`"reason": "run-completed"`,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the document wrote %s, want it to hold %s", said, want)
		}
	}
	// A document of a project that ran nothing is still a document and not an empty
	// string: a program that reads nothing has to know that it read nothing.
	empty := Queue{}.Document(at)
	if said := answerAs(t, empty); !strings.Contains(said, `"runs": []`) {
		t.Errorf("the document of nothing wrote %s, want an empty list of runs and not a null", said)
	}
}

// TestTheListOfTheWholeMachineIsOneDocument: `-all` is asked from any folder and shows the
// runs of every project of the machine. The answer is one document with one list of records
// in it, and every record says which project it is of — a program that reads two projects at
// once reads one format and does not have to know which of them a record came from
// (docs/DESIGN.md §6, §6a).
func TestTheListOfTheWholeMachineIsOneDocument(t *testing.T) {
	home := t.TempDir()
	machineOfTest(t, home)
	at := monday.Add(9 * time.Hour)
	runs, err := EveryProject(home, listOf(at))
	if err != nil {
		t.Fatalf("EveryProject returned an error: %v", err)
	}

	document := runs.Document(at)

	said := answerAs(t, document)
	for _, want := range []string{
		`"version": 1`,
		`"repo": "naghuale/crewflow"`,
		`"repo": "naghuale/tele"`,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the document of the whole machine wrote no %s, want the runs of every project of it in one answer",
				want)
		}
	}
	// The project of a document of the whole machine is the whole machine and not the
	// folder the question was asked in: a list of every project says of no one project.
	if document.Repo != "" {
		t.Errorf("the document names the project %q, want no one: a list of the whole machine is of no project", document.Repo)
	}
	if len(document.Runs) != len(runs.Entries) {
		t.Errorf("the document holds %d records, want the %d of the list", len(document.Runs), len(runs.Entries))
	}
	// A list of the whole machine has no queue of its own, and the list of the attention of
	// the document is named and empty: у каждого проекта машины своя очередь, и очередь без
	// проекта сказала бы, что никого нигде не ждёт (docs/DESIGN.md §6a).
	if document.Attention == nil || len(document.Attention) != 0 {
		t.Errorf("the document holds the attention %+v, want it empty and named", document.Attention)
	}
	// A list of one project is a document of that project, and the branch its runs are
	// counted from is in the root of it: a program has to know which branch the work is on
	// its way to.
	one, err := List(home, "naghuale-tele", listOf(at))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	one.Branch = "main"
	ofProject := one.Document(at)
	if ofProject.Repo != "naghuale/tele" || ofProject.Branch != "main" {
		t.Errorf("the document of a project = %q on %q, want naghuale/tele on main", ofProject.Repo, ofProject.Branch)
	}
	if len(ofProject.Runs) == 0 {
		t.Error("the document of a project that was run holds no records, want the runs of it")
	}
}

// TestTheAttentionOfEveryProjectIsOneDocumentOfTheMachine: `-all` у очереди — тот же
// документ, в котором записи всех проектов машины, и проект назван в каждой записи.
//
// The queue of every project is worked out of its state alone: у каждого проекта свой хостинг
// и своя очередь, а спрашивать чужой хостинг о чужом проекте — значит ответить неправдой о
// работе чужого репозитория (§6, §6a, §7g).
func TestTheAttentionOfEveryProjectIsOneDocumentOfTheMachine(t *testing.T) {
	home := t.TempDir()
	ended := monday.Add(8 * time.Hour)
	// Два проекта, у каждого прогон, который открыл изменение и которого никто не смотрел,
	// и прогон, который стоит: обе причины видны из состояния и без хостинга.
	for _, project := range []struct {
		repo    string
		ended   int
		standin int
	}{
		{repo: "naghuale-crewflow", ended: 43, standin: 44},
		{repo: "naghuale-tele", ended: 7, standin: 8},
	} {
		writeState(t, home, project.repo, project.ended, "открыл изменение", &Change{Number: 90},
			try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
		writeState(t, home, project.repo, project.standin, "стоит", nil,
			try{startedAt: ended, outcome: Running, pid: 100, lastAt: ended.Add(-47 * time.Minute),
				lastStep: "go test ./..."})
	}
	at := ended.Add(time.Hour)
	queues, err := QueuesOfEveryProject(t.Context(), home, attentionEnvOf(at, 100))
	if err != nil {
		t.Fatalf("QueuesOfEveryProject returned an error: %v", err)
	}
	if len(queues) != 2 {
		t.Fatalf("the machine holds the queues %v, want one of each of the two projects", queues)
	}

	document := MachineOf(queues).AttentionOfEveryProject(at)

	if document.Repo != "" || len(document.Runs) != 0 {
		t.Errorf("the document is of %q with %d runs, want no project and no runs: ответ о всей машине",
			document.Repo, len(document.Runs))
	}
	// The document holds every record of every project, and each of them names its own
	// project: без этого программа не знала бы, чей это прогон (§6a).
	ofProject := map[string][]int{}
	for _, one := range document.Attention {
		ofProject[one.Repo] = append(ofProject[one.Repo], one.Task)
		for _, check := range []struct {
			what  string
			value string
		}{{"attention_state", string(one.State)}, {"reason", one.Reason}} {
			if (check.what == "reason" && !KnownReason(check.value)) ||
				(check.what != "reason" && !KnownState(AttentionState(check.value))) {
				t.Errorf("the record of the task %d holds %s = %q, want a code of the closed list",
					one.Task, check.what, check.value)
			}
		}
	}
	for project, tasks := range ofProject {
		slices.Sort(tasks)
		ofProject[project] = tasks
	}
	want := map[string][]int{"naghuale/crewflow": {43, 44}, "naghuale/tele": {7, 8}}
	if !reflect.DeepEqual(ofProject, want) {
		t.Errorf("the document holds the records %+v,\nwant %+v: every project of the machine in one document",
			ofProject, want)
	}
	// Список прогонов назван и пуст, а не отсутствует: программа читает документ, не
	// спрашивая, есть ли поле (§6a).
	if !strings.Contains(answerAs(t, document), `"runs": []`) {
		t.Errorf("the document wrote %s, want an empty list of runs and not a null", answerAs(t, document))
	}
}

// TestTheClosedListOfStatesIsTheStatesTheQueueSortsBy: перечень состояний §6a закрытый, и
// очередь сортирует им ровно этим списком. Слово, которого в перечне нет, места в очереди
// не имеет, а состояние, у которого места нет, формату не принадлежит — иначе каждый, кто
// читает `-json`, разбирал бы свой список (docs/DESIGN.md §6a, AQ-005).
func TestTheClosedListOfStatesIsTheStatesTheQueueSortsBy(t *testing.T) {
	states := States()
	if len(states) != len(ranks) {
		t.Errorf("the closed list holds %d states and the queue has a place for %d, want the same: %v and %v",
			len(states), len(ranks), states, ranks)
	}
	for at, state := range states {
		if _, in := ranks[state]; !in {
			t.Errorf("the state %q of the closed list has no place in the queue", state)
		}
		if got := rankOf(state); got != at {
			t.Errorf("the state %q stands at the place %d of the queue, want the place %d", state, got, at)
		}
	}
	// The place after the last one is the absence of a place: a state that is not in the
	// closed list is not more urgent than a known one.
	if rankOf(AttentionState("stands-quietly")) <= len(states)-1 {
		t.Error("a state out of the closed list has a place among the states of the queue, want none")
	}
	// И документ не пишет состояние вне перечня, даже если запись собрана руками: очередь
	// такого слова не назовёт, а формат не обязана держать то, мимо чего прошла (§6a).
	hand := Queue{Entries: []Attention{{
		Task: 43, State: AttentionState("stands-quietly"), Reason: ReasonNoProgress,
	}}}.Document(monday)
	if said := answerAs(t, hand); strings.Contains(said, "stands-quietly") {
		t.Errorf("the document wrote %s, want no state out of the closed list in it", said)
	}
}

// TestTheClosedListOfReasonsHoldsEveryCodeOfTheQueue: перечень причин §6a закрытый, и все
// причины, которые называет очередь, в нём есть. Слово, которое очередь назвала причиной и
// которого в перечне нет, формату не принадлежит: программа, разбирающая причины по
// перечню, не обязана уметь разобрать всё, что попало в поле (docs/DESIGN.md §6a).
func TestTheClosedListOfReasonsHoldsEveryCodeOfTheQueue(t *testing.T) {
	// Каждая причина написана здесь своими словами: код, добавленный в attention.go и
	// забытый здесь, роняет этот тест — это и есть проверка полноты перечня.
	codes := []string{
		ReasonRunCompleted, ReasonRunEnded, ReasonRunTimeout, ReasonRunFailed, ReasonOutOfScope,
		ReasonNoProgress, ReasonHumanAuthorization, ReasonReviewRequired, ReasonOwnerAcceptance,
		ReasonResourceWait, ReasonBlockedPermission, ReasonBlockedSecret, ReasonBlocked,
		ReasonStoppedByHand, ReasonTaskMissing, ReasonReadFailed, ReasonReadInterrupted,
		ReasonChangeMerged, ReasonChangeClosed, ReasonTaskClosed,
		ReasonConflictWithMain, ReasonNoChecks, ReasonChecksRunning, ReasonWaitedTooLong,
		ReasonProviderUnavailable, ReasonProviderErrorUnknown, ReasonRouteUnavailable,
	}
	reasons := Reasons()
	if len(reasons) != len(codes) {
		t.Errorf("the closed list holds %d reasons and the queue names %d, want the same: %v and %v",
			len(reasons), len(codes), reasons, codes)
	}
	for _, code := range codes {
		if !slices.Contains(reasons, code) {
			t.Errorf("the reason %q is not in the closed list %v", code, reasons)
		}
		if !KnownReason(code) {
			t.Errorf("the reason %q is a code of the queue and the closed list does not know it", code)
		}
	}
	// The list is in one order and without a repeat: a repeat in a closed list is a code
	// about which nobody can say where it is and where it is not.
	if !slices.IsSorted(reasons) {
		t.Errorf("the closed list of reasons = %v, want it in one order, so that a person reads it as a table", reasons)
	}
	if slices.Contains(reasons, "") {
		t.Error("the closed list of reasons holds an empty word, want every reason named")
	}
	if KnownReason("ci-quota-exhausted") {
		t.Error("a reason out of the closed list is known to the format, want it not")
	}
}

// TestAReasonOutOfTheClosedListIsNotWritten: причина, которой в перечне §6a нет, в
// документ не попадает — иначе каждая команда, читающая состояние процесса, считала бы
// свой список причин. Слово прогона при этом не теряется: оно остаётся в объекте ожидания,
// где его читает человек, а кодом остаётся `blocked` — прогон остановился сам, и какой
// именно отказ это было, перечень не знает (docs/DESIGN.md §6a, §7e).
func TestAReasonOutOfTheClosedListIsNotWritten(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	// Прогон в режиме бота остановился на квоте CI, которой в перечне нет: своего слова
	// у этой причины в формате нет, и crewflow не заводит для неё нового.
	writeState(t, home, repo, 43, "кончились минуты CI", nil,
		try{startedAt: started, endedAt: started.Add(time.Minute), outcome: Blocked,
			reason: "ci-quota-exhausted: 403"})
	at := started.Add(2 * time.Hour)

	one := attentionOnly(t, home, repo, attentionEnvOf(at, 100))

	if one.Reason != ReasonBlocked {
		t.Errorf("the reason = %q, want %q: слово вне перечня не пишется в поле причины (§6a)", one.Reason, ReasonBlocked)
	}
	if one.State != AttentionBlocked {
		t.Errorf("the state = %q, want %q", one.State, AttentionBlocked)
	}
	if !strings.Contains(one.Subject, "ci-quota-exhausted") {
		t.Errorf("the subject of the wait = %q, want the words of the refusal in it: очередь не теряет то, что знает",
			one.Subject)
	}
	// Ни в блоке внимания, ни в записи списка, ни в блоке «не прочитано» слова вне
	// перечня нет: документ один и перечень один. Проверяется не текстом, а разбором:
	// программа читает поле `reason` и перебирает перечень, и слово, которого в перечне
	// нет, для неё — то же, что его нет вовсе.
	document := Queue{Repo: ownerAndRepo(repo), Entries: []Attention{one}}.Document(at)
	said := answerAs(t, document)
	for _, reason := range reasonsIn(t, said) {
		if !KnownReason(reason) {
			t.Errorf("the document wrote the reason %q, want a code of the closed list %v", reason, Reasons())
		}
	}
	if !strings.Contains(said, `"reason": "blocked"`) {
		t.Errorf("the document wrote %s, want the code of §6a in place of the word it does not know", said)
	}
	// The same is true of the record of a list of runs: у записи списка своя причина
	// ожидания, и она из того же перечня.
	runs := Runs{Repo: repo, Entries: []Entry{{Repo: repo, Task: 43, Attention: &one}}}.Document(at)
	for _, reason := range reasonsIn(t, answerAs(t, runs)) {
		if !KnownReason(reason) {
			t.Errorf("the record of the list wrote the reason %q, want a code of the closed list", reason)
		}
	}
}

// reasonsIn is every reason a document wrote, wherever it wrote one: и в записях прогонов, и
// в записях внимания, и в блоке «не прочитано» — перечень один на все три.
func reasonsIn(t *testing.T, said string) []string {
	t.Helper()
	var document struct {
		Runs []struct {
			Reason string `json:"reason"`
		} `json:"runs"`
		Attention []struct {
			Reason string `json:"reason"`
		} `json:"attention"`
		Unread []struct {
			Reason string `json:"reason"`
		} `json:"unread"`
	}
	if err := json.Unmarshal([]byte(said), &document); err != nil {
		t.Fatalf("the answer %s is not a document: %v", said, err)
	}
	var reasons []string
	for _, entry := range document.Runs {
		reasons = append(reasons, entry.Reason)
	}
	for _, entry := range document.Attention {
		reasons = append(reasons, entry.Reason)
	}
	for _, one := range document.Unread {
		reasons = append(reasons, one.Reason)
	}
	return reasons
}

// TestTheRecordCarriesTheReservedFieldsOfTheFormat: поля формата, которые наполняет
// nobody, названы в структуре заранее — программа, читающая `-json`, не должна ждать поля,
// которого не было вчера и будет послезавтра (docs/DESIGN.md §6a).
func TestTheRecordCarriesTheReservedFieldsOfTheFormat(t *testing.T) {
	reserved := map[string][]string{
		// Что трекер знает о задаче: веха, приоритет, зависимости, замок и кто его держит
		// (#49, #56), номер репозитория (#120) и точка, с которой прогон идёт дальше
		// (#117).
		"task_facts": {"resume", "checkpoint", "repository_id", "depends_on", "priority",
			"milestone", "lock_mode", "holder"},
		// Диагноз запроса человека и ресурс проекта, о котором он (#78).
		"attention": {"resource", "resource_type", "channel", "action"},
	}
	for record, names := range reserved {
		fields := fieldsOf(t, record)
		for _, name := range names {
			if !slices.Contains(fields, name) {
				t.Errorf("the record holds no field %q under %q, want the field of the format reserved there: %v",
					name, record, fields)
			}
		}
	}
	// A reserved field need not be filled: an empty record writes none of them, or a
	// document about a run crewflow knows nothing of would answer in words about the
	// milestones and the locks it has not heard of.
	empty := answerAs(t, Runs{Entries: []Entry{{Repo: "naghuale-crewflow", Task: 43}}}.Document(monday))
	if strings.Contains(empty, `"task_facts"`) {
		t.Errorf("the record of a run wrote %s, want nothing of a task crewflow knows nothing about", empty)
	}
	// A filled field is written under its own name: the format is one, and a word of the
	// table of §6a is a word of the document.
	filled := answerAs(t, Runs{Entries: []Entry{{Repo: "naghuale-crewflow", Task: 43, TaskFacts: &TaskFacts{
		Resume:       "crewflow task resume 43",
		Checkpoint:   &Checkpoint{Step: stepReadKey, Head: "abcdef1", At: monday},
		RepositoryID: "1234567",
		DependsOn:    []int{41, 42},
		Priority:     "p1",
		Milestone:    "v1.1",
		LockMode:     "exclusive",
		Holder:       "naghuale-crewflow#43-1",
	}}}}.Document(monday))
	for _, want := range []string{
		`"resume": "crewflow task resume 43"`, `"step": "read-executor-key"`, `"repository_id": "1234567"`,
		`"depends_on": [`, `"priority": "p1"`, `"milestone": "v1.1"`, `"lock_mode": "exclusive"`,
		`"holder": "naghuale-crewflow#43-1"`,
	} {
		if !strings.Contains(filled, want) {
			t.Errorf("the record wrote %s, want %s in it: a reserved field is written under its own name", filled, want)
		}
	}
	// The resource of the project a run waits for and the kind of it are beside each other,
	// and the kind is not invented: the model of resources is a task of its own (#78), and
	// the queue names the kind only where the source of the wait named it.
	entry := Entry{Repo: "naghuale-crewflow", Task: 43, Attention: &Attention{
		Task: 43, State: AttentionAwaitsResource, Reason: ReasonResourceWait,
		Resource: "minutes of CI", ResourceType: "ci-minutes",
	}}
	said := answerAs(t, Runs{Entries: []Entry{entry}}.Document(monday))
	for _, want := range []string{`"resource": "minutes of CI"`, `"resource_type": "ci-minutes"`} {
		if !strings.Contains(said, want) {
			t.Errorf("the record of a run wrote %s, want %s in it", said, want)
		}
	}
	entry.Attention.ResourceType = ""
	if said := answerAs(t, Runs{Entries: []Entry{entry}}.Document(monday)); strings.Contains(said, "resource_type") {
		t.Errorf("the record wrote %s, want no kind of a resource the source of the wait did not name", said)
	}
}

// TestTheBlockForAPersonIsWrittenFromTheSameRecords: человеку печатается то же, что в
// документе, — из тех же записей. Список, который читает человек, и тот, который читает
// программа, должны называть одну и ту же задачу одними и теми же словами, иначе в одном
// месте «нужно посмотреть», а в другом «не смотреть» (docs/DESIGN.md §6a, F-105).
func TestTheBlockForAPersonIsWrittenFromTheSameRecords(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	// Прогон у окна связки ключей: в строке есть всё, что запись держит в себе, и
	// ничего, чего в ней нет.
	writeState(t, home, repo, 41, "прогон у окна", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started,
			lastStep: "the executor of the run", reason: reasonApproval})
	at := started.Add(2 * time.Minute)
	queue := queueOf(t, home, repo, attentionEnvOf(at, 100), nil)

	// The document is built first on purpose: a document that cleans the codes out of the
	// records a caller keeps would take the words of a person with it, and the two must be
	// the same records (docs/DESIGN.md §6a).
	document := queue.Document(at)
	if len(document.Attention) != 1 {
		t.Fatalf("the document holds %+v, want the one record the block for a person was written from", document.Attention)
	}
	if len(queue.Entries) != 1 || queue.Entries[0].Reason != document.Attention[0].Reason {
		t.Fatalf("the queue of the caller holds %v, want the record the document was written from: %+v",
			queue.Entries, document.Attention)
	}
	var out bytes.Buffer
	if err := queue.Write(&out, screenAt(at)); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	one := document.Attention[0]
	said := out.String()
	for _, want := range []string{
		attentionHeading,
		"#" + strconv.Itoa(one.Task),
		string(one.State),
		one.Reason,
		"channel " + one.Channel,
		"the resource " + one.Resource,
		"the action " + one.Action,
		"waiting " + Idle(one.Waited()),
		"next: " + one.NextActor,
		"the last step: " + one.LastStep,
		one.Next,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the block wrote %q, want %q: the line of a person is written from the record", said, want)
		}
	}
	// And the other way about: in the document there is no number the line did not name —
	// both sides read one record.
	asJSON := answerAs(t, document)
	for _, want := range []string{strconv.Itoa(one.Task), string(one.State), one.Reason, one.Channel, one.Next} {
		if !strings.Contains(asJSON, want) {
			t.Errorf("the document wrote %s, want %q in it: the document and the line are of one record", asJSON, want)
		}
	}
	// The record under the task is of the same record too: the words of the queue and the
	// words of its notice must not drift apart.
	notice := NoticeUnder(one)
	for _, want := range []string{one.Reason, one.NextActor, one.Channel, one.Resource, one.Action} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice under the task wrote %q, want %q in it", notice, want)
		}
	}
}

// fieldsOf is the names the fields of a record of the format are written under, as the JSON
// tags of them: a program reads the document by these words, and the table of §6a says of
// the same ones.
func fieldsOf(t *testing.T, record string) []string {
	t.Helper()
	var names []string
	switch record {
	case "task_facts":
		names = tagsOf(TaskFacts{})
	case "attention":
		names = tagsOf(Attention{})
	default:
		t.Fatalf("the record %q is not one of the records of the format", record)
	}
	if len(names) == 0 {
		t.Fatalf("the record %q holds no fields at all", record)
	}
	return names
}

// tagsOf is every name a field of a record is written under, in the order of the fields.
func tagsOf(record any) []string {
	kind := reflect.TypeOf(record)
	names := make([]string, 0, kind.NumField())
	for at := range kind.NumField() {
		tag := kind.Field(at).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}

// answerAs is a document as a command writes it, and it fails the test where the document
// cannot be written at all: a document that does not write is not a document.
func answerAs(t *testing.T, answer any) string {
	t.Helper()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(answer); err != nil {
		t.Fatalf("print the answer as JSON: %v", err)
	}
	return out.String()
}
