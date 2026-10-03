package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/changelog"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge/github/app"
	"github.com/naghuale/crewflow/internal/gate"
	"github.com/naghuale/crewflow/internal/merge"
	"github.com/naghuale/crewflow/internal/network"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
	"github.com/naghuale/crewflow/internal/task"
)

// theDocumentsAreWhatTheCommandsPublish: every kind of document a command publishes with `-json`,
// with the value it publishes as it stands. It is the whole list of the registry of the boundary
// read from the commands themselves: a kind nobody publishes is a kind nobody needs, and a
// document a command publishes that is not in this table is a document the boundary would refuse
// to publish (D-082, docs/DESIGN.md §7e).
func theDocumentsAreWhatTheCommandsPublish() []struct {
	doc   secret.Document
	value any
} {
	type networkAnswer = struct {
		Route        network.Route   `json:"route"`
		Capabilities []network.State `json:"capabilities"`
	}
	type authAnswer = struct {
		Mode         string            `json:"mode"`
		Description  string            `json:"description"`
		Subject      string            `json:"subject"`
		AppID        int64             `json:"app_id"`
		Key          bool              `json:"key"`
		Installation *app.Installation `json:"installation,omitempty"`
	}
	return []struct {
		doc   secret.Document
		value any
	}{
		{doc: secret.DocumentTaskList, value: taskrun.Document{}},
		{doc: secret.DocumentTaskAttention, value: taskrun.Document{}},
		{doc: secret.DocumentTaskAttentionAll, value: taskrun.Document{}},
		{doc: secret.DocumentTaskCheck, value: task.Readiness{}},
		{doc: secret.DocumentTaskRun, value: taskrun.Result{}},
		{doc: secret.DocumentTaskResume, value: taskrun.Result{}},
		{doc: secret.DocumentTaskAdmit, value: admissionAnswer{}},
		{doc: secret.DocumentTaskCheckStalled, value: []taskrun.Standing{}},
		{doc: secret.DocumentReview, value: gate.Summary{}},
		{doc: secret.DocumentMerge, value: merge.Result{}},
		{doc: secret.DocumentVerify, value: merge.Verification{}},
		{doc: secret.DocumentDoctor, value: doctor.Report{}},
		{doc: secret.DocumentDoctorNetwork, value: []doctor.Check{}},
		{doc: secret.DocumentNetworkProxyList, value: []profileShown{}},
		{doc: secret.DocumentNetworkProxyTest, value: networkAnswer{}},
		{doc: secret.DocumentChangelogCheck, value: changelog.Answer{}},
		{doc: secret.DocumentAuthAppCheck, value: authAnswer{}},
		{doc: secret.DocumentState, value: taskrun.State{}},
	}
}

// TestEveryDocumentACommandPublishesIsInTheRegistryOfTheBoundary: the boundary publishes a
// document the caller names, and a caller that names one the registry does not know is refused
// with `output-document-policy-missing`. The refusal is a good answer, and it is also the answer
// every `-json` command of the project would give, so the list of what the commands publish is
// held against the registry of the boundary: a document added to a command is a row added to the
// registry, and a kind of the registry nobody publishes is a kind that is not needed
// (D-082, docs/DESIGN.md §7e).
func TestEveryDocumentACommandPublishesIsInTheRegistryOfTheBoundary(t *testing.T) {
	published := map[secret.Document]bool{}
	for _, one := range theDocumentsAreWhatTheCommandsPublish() {
		published[one.doc] = true
	}
	for _, doc := range secret.Documents() {
		if !published[doc] {
			t.Errorf("the registry of the boundary holds %q, and no command publishes it: a kind of document "+
				"nobody publishes is a kind nobody needs", doc)
		}
	}
}

// TestEveryFieldOfEveryDocumentIsNamedByItsPolicy: a document is refused whole when one of its
// fields is not named by the policy of that document, and the fields of a document are what the
// types the commands publish say. So the types are walked the way `encoding/json` walks them —
// the names of the fields, the elements of the lists and the shapes the marshallers of the
// project write — and every field of them has to be named: a field nobody classified is a field
// nobody looked at, and a command that publishes one answers nothing at all (D-082, R224-008,
// docs/DESIGN.md §7e).
func TestEveryFieldOfEveryDocumentIsNamedByItsPolicy(t *testing.T) {
	for _, one := range theDocumentsAreWhatTheCommandsPublish() {
		t.Run(string(one.doc), func(t *testing.T) {
			paths := map[string]string{}
			walkPaths(reflect.TypeOf(one.value), "", paths, map[reflect.Type]bool{})
			for path, marshalled := range marshalledLeavesOf(one.doc) {
				paths[path] = marshalled
			}
			names := make([]string, 0, len(paths))
			for name := range paths {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if _, _, known := secret.ClassOf(one.doc, name); !known {
					t.Errorf("the field %q of the document %q is not named by its policy", name, one.doc)
				}
			}
		})
	}
}

// walkPaths is the paths of the fields of a type as `encoding/json` writes them: the name of the
// tag of a field, the elements of a list under `[]`, the elements of a map whose keys the schema
// declares as dynamic under `.*`, and the fields of an embedded struct under the name of the
// field that holds it — an embedded struct without a name in its tag is written as the fields
// of the object around it.
func walkPaths(value reflect.Type, path string, into map[string]string, seen map[reflect.Type]bool) {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		walkPaths(value.Elem(), path+"[]", into, seen)
		return
	case reflect.Map:
		into[path+".*"] = "map"
		walkPaths(value.Elem(), path+".*", into, seen)
		return
	}
	if value.Kind() != reflect.Struct {
		if path != "" {
			into[path] = value.Kind().String()
		}
		return
	}
	if value.Implements(reflect.TypeOf((*json.Marshaler)(nil)).Elem()) {
		if path != "" {
			into[path] = "marshalled"
		}
		return
	}
	if seen[value] {
		return
	}
	seen[value] = true
	defer delete(seen, value)
	if path != "" {
		into[path] = "container"
	}
	for at := range value.NumField() {
		field := value.Field(at)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" || field.PkgPath != "" {
			continue
		}
		if name == "" && field.Anonymous {
			walkPaths(field.Type, path, into, seen)
			continue
		}
		if name == "" {
			name = field.Name
		}
		walkPaths(field.Type, ofPath(path, name), into, seen)
	}
}

// ofPath is the path of a field: the segments of a path are joined by a dot, and the root of the
// document is the empty path (D-082, docs/DESIGN.md §7e).
func ofPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// marshalledLeavesOf are the fields that stand inside a marshaller of the project: a type with a
// marshaller of its own writes a document of its own, and the boundary reads the tree of the
// document it wrote — so the fields of that document are named here, by hand, from the marshaller
// (R224-009, docs.DESIGN.md §7e).
func marshalledLeavesOf(doc secret.Document) map[string]string {
	return map[secret.Document]map[string]string{
		// `run.Entry` writes the record of a run of the list: the words of the list are the
		// outcome, the moment, the executor, the change request and the attention of the task
		// under its own name (internal/run/list.go).
		secret.DocumentTaskList: {
			"runs[]":                  "",
			"runs[].repo":             "",
			"runs[].task":             "",
			"runs[].title":            "",
			"runs[].attempts":         "",
			"runs[].run":              "",
			"runs[].outcome":          "",
			"runs[].started_at":       "",
			"runs[].ended_at":         "",
			"runs[].duration_seconds": "",
			"runs[].stalled_for":      "",
			"runs[].last_step":        "",
			"runs[].reason":           "",
			"runs[].executor":         "",
			"runs[].identity":         "",
			"runs[].change":           "",
			"runs[].task_facts":       "",
			"runs[].attention":        "",
		},
		// `run.Identity` writes the mode alone: the line of the report is in the report and
		// not in the state of the task (internal/run/state.go).
		secret.DocumentState: {"attempts[]": "", "attempts[].identity": ""},
		// `changelog.Problem` writes where it is, what is wrong with it and the name of the
		// reason (internal/changelog/project.go).
		secret.DocumentChangelogCheck: {
			"problems":          "",
			"problems[]":        "",
			"problems[].path":   "",
			"problems[].line":   "",
			"problems[].what":   "",
			"problems[].reason": "",
		},
	}[doc]
}

// TestEveryPublicationNamesADocumentOfTheRegistry: a document is published by a program that
// names it, and the name is a constant of the registry — a document assembled of a variable, a
// document built out of the name of a command, a document read out of the answer of the host are
// documents nobody wrote a policy for, and they are refused whole. The check reads the sources
// of the program: every call that publishes a document names a constant of the registry, and the
// signature of `Report` asks for one, so a publication without a document does not build
// (D-082, docs/DESIGN.md §7e).
func TestEveryPublicationNamesADocumentOfTheRegistry(t *testing.T) {
	found := 0
	// The sources of the project are read from the folder the test runs in, which is the
	// package of the command, and the root of the project is the one folder above that has a
	// file of the module in it.
	root := theRootOfTheProject(t)
	for _, folder := range []string{"cmd/crewflow", "internal"} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, declaration := range file.Decls {
				fn, is := declaration.(*ast.FuncDecl)
				if !is || fn.Body == nil {
					continue
				}
				// The two functions that take a document and pass it on are the ones place
				// where the document of the caller is named: a call inside them is the
				// forwarding of it and not a publication of its own (D-082).
				if fn.Name.Name == "printJSON" || fn.Name.Name == "Report" {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, is := node.(*ast.CallExpr)
					if !is {
						return true
					}
					name, at := publicationOf(call)
					if at < 0 {
						return true
					}
					if len(call.Args) <= at {
						t.Errorf("%s: %s is called without a document", path, name)
						return true
					}
					if !aConstantOfTheRegistry(call.Args[at]) {
						t.Errorf("%s: %s is called with %s, want a constant of the registry of the documents",
							path, name, saidOf(call.Args[at]))
						return true
					}
					found++
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("read the sources of %s: %v", folder, err)
		}
	}
	if found == 0 {
		t.Error("no call that publishes a document was found in the sources: the check holds nothing")
	}
}

// theRootOfTheProject is the folder the sources of the project stand in: the one folder above
// the package of the test that holds the file of the module.
func theRootOfTheProject(t *testing.T) string {
	t.Helper()
	folder, err := os.Getwd()
	if err != nil {
		t.Fatalf("the folder of the test: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(folder, "go.mod")); err == nil {
			return folder
		}
		above := filepath.Dir(folder)
		if above == folder {
			t.Fatal("the root of the project is not above the package of this test")
		}
		folder = above
	}
}

// publicationOf is a call that publishes a document and which of its arguments names the
// document: `printJSON` of a command and `Report` of the boundary name it first.
func publicationOf(call *ast.CallExpr) (string, int) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name == "printJSON" {
			return fun.Name, 1
		}
	case *ast.SelectorExpr:
		if fun.Sel.Name == "Report" {
			return fun.Sel.Name, 0
		}
	}
	return "", -1
}

// aConstantOfTheRegistry is whether the argument is a constant of the registry of the documents:
// a selector of the form `secret.DocumentTaskRun`, or a bare `DocumentTaskRun` inside the package
// that holds the registry. A name assembled at run time is not a constant of anything (D-082).
func aConstantOfTheRegistry(arg ast.Expr) bool {
	switch value := arg.(type) {
	case *ast.SelectorExpr:
		return strings.HasPrefix(value.Sel.Name, "Document")
	case *ast.Ident:
		return strings.HasPrefix(value.Name, "Document")
	}
	return false
}

// saidOf is what a test says about an argument that is not a constant of the registry.
func saidOf(arg ast.Expr) string {
	switch value := arg.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		if pkg, is := value.X.(*ast.Ident); is {
			return pkg.Name + "." + value.Sel.Name
		}
		return value.Sel.Name
	case *ast.BasicLit:
		if unquoted, err := strconv.Unquote(value.Value); err == nil {
			return unquoted
		}
		return value.Value
	case *ast.CallExpr:
		return called(value) + "()"
	}
	return "a value nobody can read from here"
}

// called is the name of the function a call is a call of, without the package it is written in.
func called(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}
