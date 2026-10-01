package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Kind is what kind of account of a host a subject is, in the words the host writes it
// in: "User" for a person and "Bot" for an account of an App of the host of its own.
// Both are read and nothing else is, because a kind crewflow does not know is an answer
// it may not read as «a person» (docs/DESIGN.md §7i).
type Kind string

const (
	// KindUser is an account of a host that is a person.
	KindUser Kind = "User"
	// KindApp is an account of a host that is an App of it: what the API of GitHub
	// writes as "Bot".
	KindApp Kind = "Bot"
)

// botSuffix is what the host appends to the login of every account that is an App of it.
// It is never read out of a login to decide anything: a person may have a login that
// ends in it, and a record of that person is a record of that person (docs/DESIGN.md
// §7h, §7i).
const botSuffix = "[bot]"

// Subject is an account of the host of the code as the core knows it: the kind of that
// account, and the number the host keeps it under. The powers of a subject follow that
// number and not the name the host writes it under — an App may be renamed, and every
// API of a host may write one account in a different line (docs/DESIGN.md §7i).
//
// The number is not one and the same thing for the two kinds, and that is the whole of
// the model: a person is kept by the number of the account, and an App by the number of
// the App it acts as (`performed_via_github_app.id` of the API of GitHub). A subject
// whose number crewflow could not learn is a subject that counts for nothing: it is
// never read as a person, and it never matches anybody (docs/DESIGN.md §7h, §7i).
type Subject struct {
	// Kind is what kind of account the host holds it as: [KindUser] or [KindApp].
	Kind Kind
	// ID is the number the host keeps the subject under — the number of the account for
	// a person, and the number of the App for an App. Zero means the host did not say,
	// and a subject with no number is one no record may be counted by.
	ID int64
	// Login is the name the host writes the subject under. It is here for a report and
	// for nothing else: two subjects with the same login and different numbers are two
	// accounts, and two subjects with the same number are one account whatever the host
	// calls them (docs/DESIGN.md §7h, §7i).
	Login string
}

// SubjectFrom reads the subject of an account of the host out of what the host says about
// that record: the login it writes the account under, the kind it holds the account as,
// the number it keeps the account under, and the number of the App the account acted as
// — nothing else. It is where the kinds are read and where they are refused, because a
// kind crewflow does not know is an answer crewflow must not read as «a person».
//
// An App the host names no number for is a subject with no number and not an error: a
// record of a bot that did not act as an App is a fact about the change and not a
// subject anybody may count a record by (docs/DESIGN.md §7h).
func SubjectFrom(login, kind string, id, app int64) (Subject, error) {
	if login == "" {
		return Subject{}, errors.New("the answer of the host names no account")
	}
	switch Kind(kind) {
	case KindUser:
		if strings.HasSuffix(login, botSuffix) {
			return Subject{}, fmt.Errorf("the account %q is named a person and is called an app", login)
		}
		if id <= 0 {
			return Subject{}, fmt.Errorf("the person %q is named no number by the host", login)
		}
		return Subject{Kind: KindUser, ID: id, Login: login}, nil
	case KindApp:
		// The number of the account of a bot is not the number of the App it acts as,
		// and only the second is what the settings of a project name: the App of the
		// executor and the App of the orchestrator are told apart by their number.
		return Subject{Kind: KindApp, ID: app, Login: login}, nil
	default:
		return Subject{}, fmt.Errorf("the account %q is of a kind the host names and crewflow does not know: %q",
			login, kind)
	}
}

// Same is whether two subjects are one account: the same kind of account and the same
// number. A subject the host named no number for is never the same as anybody, and a
// login is never part of the answer — a person whose login is the slug of an App is not
// that App, and the App under whatever name it is written is (docs/DESIGN.md §7h, §7i).
func (s Subject) Same(other Subject) bool {
	return s.Kind == other.Kind && s.ID != 0 && s.ID == other.ID
}

// String is the subject as a report shows it: the name the host writes it under, and the
// number it is counted by where the host named none — a subject nobody could name is
// said so rather than shown blank (docs/DESIGN.md §7i).
func (s Subject) String() string {
	if s.Login != "" {
		return s.Login
	}
	if s.ID != 0 {
		return fmt.Sprintf("%s %d", s.Kind, s.ID)
	}
	return "nobody"
}

// Named is the subject as a line of a report about the correspondence of a project shows
// it: the name the settings of the project write it under, and the kind and the number
// the gate counts it by. It is what `crewflow doctor` prints for every account of both
// lists, because a list of names cannot say what the gate will compare (docs.DESIGN.md
// §7i, §7k).
func (s Subject) Named() string {
	if s.Login == "" {
		return s.String()
	}
	return fmt.Sprintf("%s (%s %d)", s.Login, s.Kind, s.ID)
}

// Naming is the role of the host of the code that can say what subject of it a login of
// the settings of a project is: the kind of that account and the number the host keeps
// it under. It is asked once of every login the file of a project names, and the gate is
// given subjects instead of names from then on (docs/DESIGN.md §7h, §7i).
type Naming interface {
	// Subject returns the subject of the account of the host the login names, and an
	// error when the host has no such account or holds it as a kind crewflow does not
	// read — an account crewflow cannot name is an account whose record it may not
	// count, and a file of a project naming one is a mistake in that file.
	Subject(ctx context.Context, login string) (Subject, error)
}

// Signer is the role of the host of the code that can say which subject of it it speaks
// as — the account a record of a review is written in, and the account a merge is pushed
// as. A record of a review is a comment, and it is an approval only because of the
// account it was written in, so an account nobody can name is a record crewflow must not
// write and must not count (docs/DESIGN.md §7h, §7i).
type Signer interface {
	// SigningAs returns the subject the role speaks as: a person with the number the
	// host keeps that account under, or an App with the number of that App.
	SigningAs(ctx context.Context) (Subject, error)
}

// SigningAs is the subject the role of the host of the code speaks as, and an error where
// it cannot say: the mode of the orchestrator is what the record of a review is written
// in, and a report that showed one mode and wrote the record in another is a report of a
// decision nobody made (docs/DESIGN.md §7h, §7i).
func SigningAs(ctx context.Context, role Forge) (Subject, error) {
	signer, ok := role.(Signer)
	if !ok {
		return Subject{}, errors.New("the host of the project does not say which account it speaks as, " +
			"so no record of a review could be written in a name the gate would count")
	}
	return signer.SigningAs(ctx)
}

// SubjectOf asks the role of the host of the code what subject a login of the settings of
// a project is, and refuses a host that cannot say: a login nobody turned into a subject
// is a login whose record the gate must not count, and a caller that went on as though it
// had been would be counting records of an account it cannot name (docs/DESIGN.md §7h, §7i).
func SubjectOf(ctx context.Context, role Forge, login string) (Subject, error) {
	naming, ok := role.(Naming)
	if !ok {
		return Subject{}, errors.New("the host of the project does not say what an account of it is, " +
			"so the gate cannot be told whose record of a review counts")
	}
	return naming.Subject(ctx, login)
}
