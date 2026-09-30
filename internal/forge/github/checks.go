package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/forge"
)

// actionsApp is the app GitHub Actions runs under, and the only source a check of a
// project on GitHub is taken from: a mark written through the API of statuses is
// the mark of whoever wrote it, and a gate that took it would take a mark anybody
// can write (docs/DESIGN.md §7h).
const actionsApp = "github-actions"

// Checks returns the checks of the commit: the runs GitHub Actions and every other
// app started on it, and the marks written through the API of statuses. Both are
// read, because both are what a person sees under a commit, and the second is
// exactly the one the gate has to be able to refuse.
func (a *Adapter) Checks(ctx context.Context, sha string) ([]forge.CheckRun, error) {
	runs, err := a.checkRuns(ctx, sha)
	if err != nil {
		return nil, err
	}
	statuses, err := a.statuses(ctx, sha)
	if err != nil {
		return nil, err
	}
	checks := make([]forge.CheckRun, 0, len(runs)+len(statuses))
	for _, run := range runs {
		checks = append(checks, forge.CheckRun{
			Name:  run.Name,
			State: run.state(),
			App:   run.App.Slug,
			SHA:   run.HeadSHA,
		})
	}
	for _, status := range statuses {
		checks = append(checks, forge.CheckRun{
			Name:  status.Context,
			State: status.state(),
			// A mark of the API of statuses has no app of its own: it is the mark
			// of the account that wrote it, and the gate does not take it (§7h).
			App: "",
			SHA: status.SHA,
		})
	}
	return checks, nil
}

// checkRunJSON is one check of a commit as the API writes it, as far as a gate is
// concerned: the name it is known under, the commit it is about, how far it has
// got, and the app that reported it.
type checkRunJSON struct {
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	App        struct {
		Slug string `json:"slug"`
	} `json:"app"`
}

// state is how the check stands as the core writes it: a check that has not ended
// is pending, and one that ended in a conclusion of a check that did not pass is a
// failure, whatever the conclusion is called (docs/DESIGN.md §4).
func (c checkRunJSON) state() forge.CheckState {
	if c.Status != completed {
		return forge.CheckPending
	}
	if slices.Contains(failingConclusions, c.Conclusion) {
		return forge.CheckFailure
	}
	return forge.CheckSuccess
}

// statusJSON is one mark written on a commit through the API of statuses of GitHub:
// the name it is known under, the state it stands in, and the commit it is about.
// There is no app in it, and that is the whole of why the gate refuses it.
type statusJSON struct {
	Context string `json:"context"`
	State   string `json:"state"`
	SHA     string `json:"sha"`
}

// state is how a mark of the API of statuses stands as the core writes it.
func (s statusJSON) state() forge.CheckState {
	switch strings.ToLower(s.State) {
	case "success":
		return forge.CheckSuccess
	case "pending":
		return forge.CheckPending
	default:
		return forge.CheckFailure
	}
}

// statuses are the marks written on the commit through the API of statuses, and the
// whole answer of the API is read at once: a commit has a few of them, and the
// endpoint gives them all in one answer.
func (a *Adapter) statuses(ctx context.Context, sha string) ([]statusJSON, error) {
	out, err := a.json(ctx, "api", "repos/"+a.repo+"/commits/"+sha+"/status")
	if err != nil {
		return nil, err
	}
	var answer struct {
		// SHA is the commit the marks are about, and the API names it once for all
		// of them rather than in each: a mark of a commit that is not the one the
		// review asked about is a mark of another commit, and the gate has to be
		// able to see that (docs/DESIGN.md §7h).
		SHA      string       `json:"sha"`
		Statuses []statusJSON `json:"statuses"`
	}
	if err := decode(out, &answer); err != nil {
		return nil, err
	}
	for i := range answer.Statuses {
		answer.Statuses[i].SHA = answer.SHA
	}
	return answer.Statuses, nil
}

// Rules returns what the rules of the branch of the project demand of a change, the
// app every one of them is to come from, and what the host said about the rules
// themselves.
//
// The rules of a branch of GitHub are of two kinds and both of them are asked about,
// because a project may have either or both: the protection of the branch itself, and
// the rulesets of the repository, of its organization or of its enterprise. Both may
// be there at once, and neither may be there at all: a private repository on the free
// plan has no rulesets, GitHub says which plan it is about, and that is an answer
// about the repository rather than a silence of the host (docs/DESIGN.md §7h, §7k).
func (a *Adapter) Rules(ctx context.Context) (forge.BranchRules, error) {
	protected, err := a.protection(ctx)
	if err != nil {
		return forge.BranchRules{}, err
	}
	fromRulesets, state, err := a.rulesetChecks(ctx)
	if err != nil {
		return forge.BranchRules{}, err
	}
	// The plan of the repository has no rules of a branch to demand anything with,
	// and what it names no more than the rulesets do: an answer with nothing in it
	// is an answer, and what the project says about its CI is what stands (§7h, §7k).
	if state == forge.RulesUnavailableOnPlan {
		return forge.BranchRules{State: state}, nil
	}
	var required []forge.RequiredCheck
	for _, contexts := range [][]string{protected, fromRulesets} {
		for _, context := range contexts {
			if !slices.ContainsFunc(required, func(want forge.RequiredCheck) bool { return want.Name == context }) {
				required = append(required, forge.RequiredCheck{Name: context, App: actionsApp})
			}
		}
	}
	return forge.BranchRules{Required: required, State: forge.RulesNamed}, nil
}

// RequiredChecks returns what the rules of the branch of the project demand of a
// change, and the app every one of them is to come from — the answer of a CI of §7g
// that says nothing about the plan of the repository. GitHub says both at once, so
// this is [Adapter.Rules] with the state of the rules taken away (docs/DESIGN.md §7h).
func (a *Adapter) RequiredChecks(ctx context.Context) ([]forge.RequiredCheck, error) {
	rules, err := a.Rules(ctx)
	if err != nil {
		return nil, err
	}
	return rules.Required, nil
}

// protection are the checks the protection of the default branch of the project
// demands, and nothing when the branch is not protected: a branch of a project that
// protects nothing is a project that has left this to its CI and not to the host
// (docs/DESIGN.md §7h).
func (a *Adapter) protection(ctx context.Context) ([]string, error) {
	out, err := a.json(ctx, "api", "repos/"+a.repo+"/branches/"+a.base())
	if err != nil {
		return nil, err
	}
	var branch struct {
		Protected bool `json:"protected"`
	}
	if err := decode(out, &branch); err != nil {
		return nil, err
	}
	if !branch.Protected {
		return nil, nil
	}
	// The branch is protected and the rules of that protection are asked for
	// separately, with the status of the answer, because "this branch is not
	// protected" and "you may not read the rules of this branch" look the same to
	// a program that only reads what a command said: the first is an answer and the
	// second is the absence of one, and a gate that cannot tell them apart is a gate
	// that passes a change on a silence (docs/DESIGN.md §7h).
	answer, err := a.api(ctx, "repos/"+a.repo+"/branches/"+a.base()+"/protection")
	switch {
	case err != nil:
		return nil, err
	case answer.Status == http.StatusNotFound:
		// The branch is protected by rulesets rather than by the protection of the
		// branch itself, and the rulesets below are where its checks are.
		return nil, nil
	case answer.Status != http.StatusOK:
		return nil, fmt.Errorf("the branch %s is protected and the rules of it cannot be read: the API answered %d",
			a.base(), answer.Status)
	}
	var protection struct {
		RequiredStatusChecks struct {
			Contexts []string `json:"contexts"`
			Checks   []struct {
				Context string `json:"context"`
			} `json:"checks"`
		} `json:"required_status_checks"`
	}
	if err := decode(answer.Body, &protection); err != nil {
		return nil, err
	}
	required := slices.Clone(protection.RequiredStatusChecks.Contexts)
	for _, check := range protection.RequiredStatusChecks.Checks {
		if !slices.Contains(required, check.Context) {
			required = append(required, check.Context)
		}
	}
	return required, nil
}

// apiAnswer is one answer of the API of the host with the status of it, which is the
// difference between a host that says no and a host that says nothing: the first is
// a fact about the branch and the second is a gap in what crewflow knows about it
// (docs/DESIGN.md §7h).
type apiAnswer struct {
	// Status is what the host answered: 200, 404, 403 and whatever else.
	Status int
	// Body is what it wrote after its headers.
	Body []byte
}

// api is one call to the API with the headers of the answer in it. Every other call
// of the adapter asks gh for JSON and takes what it wrote as the answer of the host,
// which is enough while the question is one that always has one; a question about the
// rules of a branch has three answers, and which of them came is in the status.
func (a *Adapter) api(ctx context.Context, path string) (apiAnswer, error) {
	environment, err := a.speaking(ctx)
	if err != nil {
		return apiAnswer{}, err
	}
	command := []string{"api", "--include", path}
	stdout, stderr, code, err := a.env.Run(ctx, program, command, "", environment)
	if err != nil {
		return apiAnswer{}, fmt.Errorf("gh api %s: %w", path, err)
	}
	status, body, found := answerIn(stdout)
	if !found {
		return apiAnswer{}, fmt.Errorf("gh api %s: exited with %d and the answer holds no status of the host: %s",
			path, code, firstLine(stderr))
	}
	return apiAnswer{Status: status, Body: body}, nil
}

// statusLine is the first line of the headers of a response, which is the only one
// that says what the host answered: `HTTP/2.0 404 Not Found`.
var statusLine = regexp.MustCompile(`^HTTP/\S+\s+(\d{3})\b`)

// answerIn is the status of an answer and the body under its headers, and whether
// there is a status in it at all. The headers and the body are one stream, which is
// what `gh api --include` writes, and the body may be empty: a refusal with nothing
// in it is a refusal all the same.
func answerIn(out []byte) (int, []byte, bool) {
	header, body, split := strings.Cut(string(out), "\r\n\r\n")
	if !split {
		header, body, split = strings.Cut(string(out), "\n\n")
		if !split {
			header, body = string(out), ""
		}
	}
	first, _, _ := strings.Cut(header, "\n")
	match := statusLine.FindStringSubmatch(strings.TrimRight(first, "\r"))
	if match == nil {
		return 0, nil, false
	}
	status, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, nil, false
	}
	return status, []byte(body), true
}

// rulesetChecks are the checks the rulesets of the repository, of its organization
// and of its enterprise demand of its default branch, and what the host said about the
// rulesets themselves. A ruleset is asked for one by one, because the list of them
// holds no rules: a ruleset is a document of its own and the checks are in it.
//
// The list is asked for with the status of the answer in it, because it has three
// answers and which of them came is the whole of what a ruleset question asks: a list
// of rulesets, a refusal that names the plan of the repository, and a host that could
// not answer (docs/DESIGN.md §7h, §7k).
func (a *Adapter) rulesetChecks(ctx context.Context) ([]string, forge.RuleState, error) {
	path := "repos/" + a.repo + "/rulesets?includes_parents=true"
	answer, err := a.api(ctx, path)
	switch {
	case err != nil:
		return nil, "", err
	case answer.Status == http.StatusForbidden && mentionsThePlan.Match(answer.Body):
		// The plan of the repository has no rulesets to ask about: GitHub has them
		// in public repositories and in the paid ones, and it names the plan rather
		// than sending an empty list. It answered — there are none — and a gate that
		// read this as a silence would refuse every change of such a repository for
		// ever (docs/DESIGN.md §7h, §7k).
		return nil, forge.RulesUnavailableOnPlan, nil
	case answer.Status != http.StatusOK:
		return nil, "", fmt.Errorf("the rulesets of %s cannot be read: the API answered %d: %s",
			a.repo, answer.Status, said(answer.Body))
	}
	var rulesets []struct {
		ID     int64  `json:"id"`
		Target string `json:"target"`
	}
	if err := decode(answer.Body, &rulesets); err != nil {
		return nil, "", err
	}
	var required []string
	for _, ruleset := range rulesets {
		if ruleset.Target != "branch" {
			continue
		}
		out, err := a.json(ctx, "api", fmt.Sprintf("repos/%s/rulesets/%d", a.repo, ruleset.ID))
		if err != nil {
			return nil, "", err
		}
		var full struct {
			Conditions struct {
				RefName struct {
					Include []string `json:"include"`
				} `json:"ref_name"`
			} `json:"conditions"`
			Rules []struct {
				Type       string `json:"type"`
				Parameters struct {
					RequiredStatusChecks []struct {
						Context string `json:"context"`
					} `json:"required_status_checks"`
				} `json:"parameters"`
			} `json:"rules"`
		}
		if err := decode(out, &full); err != nil {
			return nil, "", err
		}
		if !covers(full.Conditions.RefName.Include, a.base()) {
			continue
		}
		for _, rule := range full.Rules {
			if rule.Type != "required_status_checks" {
				continue
			}
			for _, check := range rule.Parameters.RequiredStatusChecks {
				if check.Context != "" && !slices.Contains(required, check.Context) {
					required = append(required, check.Context)
				}
			}
		}
	}
	return required, forge.RulesNamed, nil
}

// mentionsThePlan is whether the body of a refusal of GitHub is about the plan of the
// repository: it names the plan to upgrade to or asks for the repository to be made
// public, and that is what a refusal of a feature the plan does not have looks like.
//
// It is never read on its own: a refusal with nothing of a plan in it is somebody who
// may not read the rules of the repository, and reading that as "there are no rules"
// would take a refusal of access for a fact about the repository and let a change
// through on it (docs/DESIGN.md §7h).
var mentionsThePlan = regexp.MustCompile(`(?i)upgrade to github|github (pro|team|enterprise)|` +
	`make this repository public|current plan|only available for public repositories`)

// said is what the host said in the body of a refusal: the message of the answer of
// GitHub, which is JSON, and the first line of it whatever else it is. A report is
// read by people, and a person who is told "the API answered 500" learns less than one
// who is told what the host said (docs/DESIGN.md §7h).
func said(body []byte) string {
	var refusal struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &refusal); err == nil && refusal.Message != "" {
		return refusal.Message
	}
	return firstLine(body)
}

// defaultBranchPattern is how a ruleset names the default branch of a repository
// that names no branch: the name of the branch is not in the conditions, the word
// for "whichever it is" is.
const defaultBranchPattern = "~DEFAULT_BRANCH"

// covers is whether the conditions of a ruleset are about the branch, which is what
// a ruleset that is not about it cannot be asked for. The conditions are the words
// of the host: a pattern, or the name of the branch.
func covers(patterns []string, branch string) bool {
	return slices.Contains(patterns, branch) || slices.Contains(patterns, defaultBranchPattern)
}
