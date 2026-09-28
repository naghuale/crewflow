// Package app is the GitHub App a project's executor works as: the private key of
// it, the token crewflow signs for it and the account the commits of a run come from
// (docs/DESIGN.md §7i).
//
// Everything the App needs is a plain value in [Source] — the key comes from a store,
// the API is an address and an HTTP client, and the clock is a function — so that a
// test of a token exchange runs against a server of its own and never against
// GitHub. Only the standard library signs a JWT: RS256 is a private key signature
// over a header and claims, and there is nothing here a library would know better.
package app

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// The lifetime of the JWT crewflow signs for the App, as GitHub wants it: ten minutes
// at most, and the token it buys lives an hour. A run of an agent is longer than
// that, which is why the credential helper of crewflow signs a new one for every push
// and nothing of a run keeps one (docs/DESIGN.md §7i).
const (
	issuedAgo   = 60 * time.Second
	validFor    = 9 * time.Minute
	jwtAudience = ""
)

// The names the parts of a token of GitHub are written under, in the JSON of the
// answer and in the claims of a JWT. They are the field names of the API, and crewflow
// reads only the ones it needs.
const (
	claimIssuedAt = "iat"
	claimExpiry   = "exp"
	claimIssuer   = "iss"
)

// ErrNoKey is the answer of a store that holds no key of this App, as opposed to a
// store that could not be read: the owner has to import the key in the first case and
// has a machine to look at in the second, and a report says which one it is.
var ErrNoKey = errors.New("no private key of this app in the store")

// Key is a private key of a GitHub App, in the PEM of the file a person downloaded
// from the settings of the App. Both forms GitHub gives are read: PKCS#8, which is
// what its "Generate a private key" writes today, and the PKCS#1 of the older button.
//
// The key is read once, where it is needed, and is not kept: the App signs with it
// and the rest of crewflow never holds it (docs/DESIGN.md §7i).
type Key struct {
	// key is what the JWT of the App is signed with.
	key *rsa.PrivateKey
}

// ReadKey is the private key of a GitHub App out of the PEM a person downloaded. A
// file that is not an RSA private key is refused with what was wrong with it: the key
// of an App is an RSA key, and crewflow will not import anything else under its name.
func ReadKey(pemBytes []byte) (Key, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return Key{}, errors.New("the file is not in PEM: a private key of an app begins with -----BEGIN")
	}
	if key, err := parseRSA(block.Bytes); err != nil {
		return Key{}, fmt.Errorf("%s: %w", block.Type, err)
	} else {
		return Key{key: key}, nil
	}
}

// parseRSA is a private key in either of the two forms a key of an app comes in: the
// PKCS#1 of the older button of GitHub and the PKCS#8 of the newer one. The form is
// not asked about, because the file says which it is in its own first line.
func parseRSA(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("not an RSA private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA private key, it is a %T", parsed)
	}
	return key, nil
}

// PEM is the key in the form a file holds it, for a person who has to keep the file
// they downloaded. Nothing of crewflow writes it, and a report never shows it: the
// key of an App is the one secret that must not reach a journal, an error or a
// terminal (docs/DESIGN.md §7e).
func (k Key) PEM() []byte {
	der, err := x509.MarshalPKCS8PrivateKey(k.key)
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// JWT is the token crewflow signs with the key of the App to be let in as the App
// itself: a header that says RS256, the three claims GitHub looks at and nothing
// else, and the signature of the two. The times are a minute into the past and nine
// minutes into the future, which is what GitHub refuses nothing within and accepts
// nothing outside of.
//
// The token is not a secret crewflow keeps: it says who the App is, it lives nine
// minutes, and the private key it was signed with never leaves the machine.
func (k Key) JWT(appID int64, now time.Time) (string, error) {
	header, err := encode(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", fmt.Errorf("the header of the token: %w", err)
	}
	claims, err := encode(map[string]any{
		claimIssuedAt: now.Add(-issuedAgo).Unix(),
		claimExpiry:   now.Add(validFor).Unix(),
		claimIssuer:   strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", fmt.Errorf("the claims of the token: %w", err)
	}
	signing := header + "." + claims
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, k.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign the token of the app: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// encode is one part of a JWT as the API of GitHub wants it: JSON, without padding
// and with the characters of a URL, because the whole token is one part of a header
// of an HTTP request.
func encode(part map[string]any) (string, error) {
	data, err := json.Marshal(part)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
