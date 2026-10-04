package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// The contours a project may name. The list is closed: a word nobody defined is refused
// where the file is read and by the resolver, and a fourth contour of a design that has
// three is not written down here to be opened later by a word (docs/ENVIRONMENT-CONFIG.md).
const (
	ContourDev  = "dev"
	ContourTest = "test"
	ContourProd = "prod"
)

// ContourLegacy is what a file that names no contour is called. It is not a word a person
// may write and not dev under another name: the runs of a project that says nothing are
// the runs it had before the key was there, and calling them dev would reclassify them and
// move the paths of the machine by nobody's word.
const ContourLegacy = "legacy-unclassified"

// ContourLayout is the answer of [Config.ResolveContour]: the contour of the file and the
// two roots crewflow proposes for it.
//
// It is a separate value on purpose. The resolver writes nothing back into the config,
// `Config.Worktrees.Root` keeps the value the file gave it, and nothing routes a task, a
// run or a merge by this struct until a task of its own wires it in
// (docs/ENVIRONMENT-CONFIG.md).
type ContourLayout struct {
	// Contour is the name of the contour: "dev", "test", "prod", or [ContourLegacy] for
	// a file that names none.
	Contour string
	// Declared says whether the file named the contour down. It is false with
	// Contour = [ContourLegacy] and nowhere else.
	Declared bool
	// Home is the directory the roots are relative to: the home the resolver was given,
	// which is the home of the machine it runs on only when the caller passed that one.
	Home string
	// State is the root the state of this project in this contour is proposed in.
	State string
	// Worktrees is the root the worktrees of the tasks of this contour are proposed in.
	Worktrees string
	// StateNamed says whether the file named the state root itself, and WorktreesNamed
	// the same of the worktrees root: a report has to tell a root of the file from a root
	// crewflow proposed, and a proposal is not a decision of a person.
	StateNamed     bool
	WorktreesNamed bool
}

// ResolveContour works out the file spaces of the contour of a project and refuses a file
// whose spaces cannot be told from the spaces of another contour.
//
// It is a pure function of the config and of the home directory it is given: it creates no
// folder, reads no file, asks nothing of the network and nothing of the store of secrets,
// and it writes nothing back into the config. What a command does with the answer is a
// decision of a task of its own — today nothing does, and the paths of the runtime stay
// where the file put them (docs/ENVIRONMENT-CONFIG.md).
func (c Config) ResolveContour(home string) (ContourLayout, error) {
	contour, declared, err := contourOf(c.Environment.Contour)
	if err != nil {
		return ContourLayout{}, err
	}
	repoName := c.RepoName()
	state, stateNamed, err := rootOf(
		"environment.state_root", c.Environment.StateRoot, contour, "state", repoName, home)
	if err != nil {
		return ContourLayout{}, err
	}
	worktrees, worktreesNamed, err := rootOf(
		"environment.worktrees_root", c.Environment.WorktreesRoot, contour, "worktrees", repoName, home)
	if err != nil {
		return ContourLayout{}, err
	}
	layout := ContourLayout{
		Contour:        contour,
		Declared:       declared,
		Home:           home,
		State:          state,
		Worktrees:      worktrees,
		StateNamed:     stateNamed,
		WorktreesNamed: worktreesNamed,
	}
	if err := layout.checkPeers(c); err != nil {
		return ContourLayout{}, err
	}
	return layout, nil
}

// contourOf is the classification of the file: the word it writes, or the word legacy means
// when it writes none.
//
// Only the key as it stands decides whether the file named a contour: an absent key and an
// empty one are legacy, and a key that was written with spaces in it and no word is a
// mistake a person made, not a choice to name no contour — cleaning a value before asking
// whether there is one would answer "nobody wrote a contour" about a key somebody wrote. A
// word with spaces around it and a word of nobody are refused with the cleaned word and not
// with the line as it was typed, so a refusal is the same one every time and carries no more
// than the key, the word that was meant and the three words that are there.
func contourOf(written string) (string, bool, error) {
	if written == "" {
		return ContourLegacy, false, nil
	}
	contour := strings.TrimSpace(written)
	switch {
	case contour == "":
		return "", false, fmt.Errorf("environment.contour: the key holds spaces and no word, and the name of a "+
			"contour is one word; a contour is %s, or the key out for a project that names none",
			strings.Join(contours, ", "))
	case contour != written:
		return "", false, fmt.Errorf("environment.contour: the name of a contour is one word, %q has a space around it; "+
			"a contour is %s, or the key out for a project that names none", contour, strings.Join(contours, ", "))
	}
	if err := oneOf("environment.contour", contour, contours); err != nil {
		return "", false, err
	}
	return contour, true, nil
}

// rootOf is one root of a layout and whether the project named it itself: the root a file
// wrote, checked as a path and expanded, or the root crewflow proposes for the contour.
// The words of a complete config are given one by one rather than taken from the struct,
// because the roots of the peers are the proposed ones and no config holds them.
func rootOf(key, written, contour, kind, repoName, home string) (root string, named bool, err error) {
	choice := strings.TrimSpace(written)
	if choice == "" {
		return expandRootIn(home, repoName, derivedRoot(contour, kind, repoName)), false, nil
	}
	if err := validRoot(key, choice); err != nil {
		return "", false, err
	}
	return expandRootIn(home, repoName, choice), true, nil
}

// derivedRoot is the folder crewflow proposes for one space of a contour of a project that
// names no root. The contour stands a level above the two spaces of the project, and a file
// that names no contour keeps the folders of the machine today — `~/.crewflow/state/<repo>`
// and `~/.crewflow/worktrees/<repo>`, the same words the runtime uses — because a file that
// says nothing must not move where the state and the worktrees of a run live
// (docs/ENVIRONMENT-CONFIG.md).
func derivedRoot(contour, kind, repoName string) string {
	if contour == ContourLegacy {
		return "~/.crewflow/" + kind + "/" + repoName
	}
	return "~/.crewflow/" + contour + "/" + kind + "/" + repoName
}

// expandRootIn is [Config.ExpandPath] with the home directory given: the resolver is a
// function of the file and of the home it was told about, and not of the machine it runs
// on. The rules are the same two — `{repo}` becomes the name of the repository, a leading
// "~/" becomes the home — and a test of contour_test.go holds the two to the same answer
// for the same words, because two ways of expanding one path are two places for the spaces
// of a contour to drift away from the paths of a run.
func expandRootIn(home, repoName, root string) string {
	expanded := strings.ReplaceAll(root, "{repo}", repoName)
	rest, isHomeRelative := strings.CutPrefix(expanded, "~/")
	if !isHomeRelative {
		return filepath.Clean(expanded)
	}
	return filepath.Join(home, filepath.FromSlash(rest))
}

// proposedRoot is one root of one contour together with the words a report calls it by:
// the contour it belongs to, which of the two spaces it is, and the key of the file it
// came from — empty when crewflow proposed it and nobody wrote it down.
type proposedRoot struct {
	contour string
	kind    string
	key     string
	root    string
}

// described is how a refusal names a root: the key a person wrote, or the proposal that
// stood in its place, so that the person reading the refusal knows which word of the file
// to change.
func (r proposedRoot) described() string {
	if r.key != "" {
		return r.key
	}
	return "the " + r.kind + " root crewflow proposes for the contour " + r.contour
}

// checkPeers refuses two file spaces that are one folder. A root of this contour that is
// the folder of another contour, or a folder inside it, is one space of files whatever the
// machine calls them: whatever works in one of the two writes into the other.
//
// The peers are spelled out here and they are the whole of what the check sees: the two
// roots of this contour, the two roots of every other contour crewflow knows, and the two
// roots of a file that names no contour. A space a project keeps elsewhere, a symlink, a
// mount and a permission of the machine are not in that set — so passing this check is not
// a proof that the contours of a machine are isolated, it is a proof that the folders
// this project named for them do not lie in one another (docs/ENVIRONMENT-CONFIG.md).
func (layout ContourLayout) checkPeers(c Config) error {
	own := layout.roots()
	state, worktrees := own[0], own[1]
	if sameFolder(state.root, worktrees.root) {
		return fmt.Errorf("%s: the %s of the contour %q and its %s are one folder (%s): the files of a state "+
			"among the worktrees of a task are read and written by everything that works in that folder",
			state.described(), state.kind, layout.Contour, worktrees.kind, state.root)
	}
	for _, peer := range layout.peers(c) {
		for _, mine := range own {
			if !overlaps(mine.root, peer.root) {
				continue
			}
			return fmt.Errorf("%s: the %s of the contour %q (%s) and the %s of the contour %q (%s) are one space "+
				"of files: two contours that share a folder are one folder, whatever the machine calls them",
				mine.described(), mine.kind, mine.contour, mine.root, peer.kind, peer.contour, peer.root)
		}
	}
	return nil
}

// roots are the two spaces of this layout in the order every refusal names them: the state
// first, because a state of a contour is what the words of a project are first about.
func (layout ContourLayout) roots() []proposedRoot {
	return []proposedRoot{
		{
			contour: layout.Contour, kind: "state",
			key:  namedKey(layout.StateNamed, "environment.state_root"),
			root: layout.State,
		},
		{
			contour: layout.Contour, kind: "worktrees",
			key:  namedKey(layout.WorktreesNamed, "environment.worktrees_root"),
			root: layout.Worktrees,
		},
	}
}

// peers are the roots of every contour but this one, and the roots of a file that names no
// contour, all of them the proposed ones: the roots a project names are the spaces of the
// contour it names, and a contour of the design has folders of its own.
func (layout ContourLayout) peers(c Config) []proposedRoot {
	repoName := c.RepoName()
	var peers []proposedRoot
	for _, contour := range slices.Concat(contours, []string{ContourLegacy}) {
		if contour == layout.Contour {
			continue
		}
		for _, kind := range []string{"state", "worktrees"} {
			peers = append(peers, proposedRoot{
				contour: contour,
				kind:    kind,
				root:    expandRootIn(layout.Home, repoName, derivedRoot(contour, kind, repoName)),
			})
		}
	}
	return peers
}

// namedKey is the key a root came from, and the empty string for a root crewflow proposed:
// a root of the file is named by its key and a proposal is named by nothing, which is what
// keeps a refusal about the right word of the file.
func namedKey(named bool, key string) string {
	if named {
		return key
	}
	return ""
}

// sameFolder is whether two roots are one folder as the words of a file name them. It is
// another question from overlaps: two contours may not share a folder, and the state and
// the worktrees of one contour may stand in one another as long as they are not equal.
func sameFolder(one, other string) bool {
	return filepath.Clean(one) == filepath.Clean(other)
}

// overlaps is whether two roots are one space of files: the same folder, or one of them
// inside the other. The answer is about the words of the file and not about the machine —
// no directory is read, no symlink is followed and no case of the path is folded — so it
// holds for the roots of this project and for nothing outside them.
func overlaps(one, other string) bool {
	return insideOrEqual(one, other) || insideOrEqual(other, one)
}

// insideOrEqual is whether one root lies within the other, the two cleaned first so that
// `~/spaces/./state` and `~/spaces/state` are one folder of a file.
func insideOrEqual(inner, outer string) bool {
	inner, outer = filepath.Clean(inner), filepath.Clean(outer)
	if inner == outer {
		return true
	}
	separator := string(filepath.Separator)
	if !strings.HasSuffix(outer, separator) {
		outer += separator
	}
	return strings.HasPrefix(inner, outer)
}
