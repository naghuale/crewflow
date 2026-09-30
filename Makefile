# How crewflow is put on a machine, and why the signature is a part of that.
#
# A build of Go is signed by the linker for that build alone, and the keychain of macOS
# ties the access of a program to a secret of it to the signature of that program: every
# build of it is a program the keychain has never seen, and the owner of the machine is
# asked to allow it again in a window of the system. Signing the program with a
# certificate of the owner of the machine makes the "Always Allow" of that window survive
# the next build (docs/DESIGN.md §7i).
#
#   make install SIGN_IDENTITY="crewflow (local)"
#
# The name of the identity is the one this machine prints:
#
#   security find-identity -v -p codesigning
#
# Without SIGN_IDENTITY the program is built and installed with nothing but the signature
# of the linker, and `crewflow doctor` says what that costs. The README says how a
# certificate of your own is made.

BIN ?= crewflow
# Where the program goes: the same place `go install` puts it, and the first one of PATH
# a person has on a machine that has the Go toolchain.
PREFIX ?= $(HOME)/go/bin
# The identity of codesign, and nothing else: a program is signed with the name of a
# certificate of the owner of the machine and with nothing that names it for ever.
SIGN_IDENTITY ?=
# The program of this build, where it is signed and from where it is put in place.
STAGE := $(CURDIR)/$(BIN)

.PHONY: all build sign install check

all: check

build:
	go build -o $(STAGE) ./cmd/crewflow

# sign gives the program the signature of the owner of the machine, and says what it
# signed it with. `--force` is there because every build writes the stage again, and the
# timestamp of an authority is not asked for: a certificate of the owner of the machine is
# not issued by one, and codesign must not be sent looking for it.
sign: build
ifneq ($(SIGN_IDENTITY),)
	codesign --force --sign "$(SIGN_IDENTITY)" $(STAGE)
	@codesign --display --verbose=1 $(STAGE) 2>&1 | sed -n 's/^Signature=//p' | \
		if read -r signed; then \
			if [ "$$signed" = adhoc ]; then \
				echo "crewflow: SIGN_IDENTITY did not name a certificate of your own: the program is signed for this build alone" >&2; \
				exit 1; \
			fi; \
			echo "crewflow: signed with $(SIGN_IDENTITY) ($$signed)"; \
		else \
			echo "crewflow: signed with $(SIGN_IDENTITY)"; \
		fi
else
	@echo "crewflow: built without SIGN_IDENTITY: macOS will ask again for every build (see the README)"
endif

# install is how crewflow goes onto a machine. It is the same program a person would get
# from `go install`, with the one thing `go install` cannot do: a certificate of the owner
# of the machine on it, so that the access to the keychain of an App survives the next
# build of it (docs/DESIGN.md §7i).
install: sign
	mkdir -p "$(PREFIX)"
	install -m 0755 $(STAGE) "$(PREFIX)/$(BIN)"

# check is what the gate of the project runs, in the order it runs it in.
check:
	sh -c 'test -z "$$(gofmt -l .)"'
	go vet ./...
	go test -race -count=1 ./...
	golangci-lint run --timeout 5m ./...
	go mod tidy -diff
