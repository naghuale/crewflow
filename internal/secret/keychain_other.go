//go:build !darwin || !cgo

package secret

import "fmt"

// elsewhere is the store of a machine that has none: the keychain of macOS is the only
// store crewflow has, and a mode of a run that needs one says so instead of keeping a
// key of an App in a file of the project or in the environment of the executor
// (docs/DESIGN.md §7i). A macOS without cgo is such a machine as well — the store of it
// is the framework, and a build without cgo cannot reach it.
type elsewhere struct{}

// System returns the store of this machine, which is a store that refuses everything:
// a mode of a run that works as a bot needs a key of an App to sign tokens with, and
// there is nowhere on this machine to keep one.
func System() Store { return elsewhere{} }

// Get says that there is no store here. The name of the service and of the account are
// in the error, because that is what a person has to look for in the settings of the
// project, and the reason is the whole of it.
func (elsewhere) Get(service, account string) ([]byte, error) {
	return nil, fmt.Errorf("%s/%s: no store of secrets on this machine: the keychain of macOS is the store crewflow has, and this machine has none", service, account)
}

// Set says that there is no store here, for the reason [elsewhere.Get] gives.
func (elsewhere) Set(service, account string, _ []byte) error {
	_, err := elsewhere{}.Get(service, account)
	return err
}

// Has says that there is no store here, for the reason [elsewhere.Get] gives: a
// report that cannot ask must say that it did not ask.
func (elsewhere) Has(service, account string) (bool, error) {
	_, err := elsewhere{}.Get(service, account)
	return false, err
}
