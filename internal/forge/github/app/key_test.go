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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestKeyJWTCarriesTheThreeClaimsGitHubLooksAt is the whole of what the token crewflow
// signs has to be: a header that says how the token is signed, the time it was made,
// the time it is good until, and the number of the App. GitHub refuses a token
// outside of that window and one signed for another App, and a report of a run that
// was refused says nothing of which of the two it was.
func TestKeyJWTCarriesTheThreeClaimsGitHubLookaAt(t *testing.T) {
	key, pub := keyOfTest(t)
	now := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)

	token, err := key.JWT(5107052, now)
	if err != nil {
		t.Fatalf("JWT: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the token is %q, want three parts of a JWT", token)
	}
	header := objectOfTest(t, parts[0], true)
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Errorf("the header of the token is %v, want RS256 and JWT", header)
	}
	claims := objectOfTest(t, parts[1], true)
	want := map[string]any{
		claimIssuedAt: float64(now.Add(-issuedAgo).Unix()),
		claimExpiry:   float64(now.Add(validFor).Unix()),
		claimIssuer:   "5107052",
	}
	for name, value := range want {
		if claims[name] != value {
			t.Errorf("the claim %s = %v, want %v", name, claims[name], value)
		}
	}
	if len(claims) != len(want) {
		t.Errorf("the token carries the claims %v, want only %v", claims, want)
	}
	// The signature is what makes the token a token: the public key of the pair is
	// what GitHub checks it with, and a token nobody signed is a token anybody wrote.
	if !signedWithTheKey(t, token, pub) {
		t.Error("the token is not signed with the private key of the app, want a signature the public key holds")
	}
}

// TestKeyJWTNamesTheAppItIsFor: the issuer is the number of the App, and a token of
// one App is not a token of another.
func TestKeyJWTNamesTheAppItIsFor(t *testing.T) {
	key, _ := keyOfTest(t)
	now := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)

	first, err := key.JWT(5107052, now)
	if err != nil {
		t.Fatalf("JWT: %v", err)
	}
	second, err := key.JWT(5107053, now)
	if err != nil {
		t.Fatalf("JWT: %v", err)
	}
	if first == second {
		t.Error("the token of one app is the token of another, want the number of the app in it")
	}
}

// TestKeyJWTSignedByAnotherKeyIsRefused: the check GitHub makes is the only one that
// counts, and a test that looks at the claims of a token has to make it too.
func TestKeyJWTSignedByAnotherKeyIsRefused(t *testing.T) {
	key, _ := keyOfTest(t)
	other := Key{key: anotherKeyOfTest(t)}
	now := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)

	token, err := key.JWT(5107052, now)
	if err != nil {
		t.Fatalf("JWT: %v", err)
	}
	if signedWithTheKey(t, token, &other.key.PublicKey) {
		t.Error("the token is signed with another key of the test, want the signature of its own key only")
	}
}

// TestReadKeyOfBothFormsGitHubGives: a person downloads the key of an App with the
// button of GitHub, and that button has written PKCS#8 for a few years and PKCS#1
// before that. A file that is not an RSA key is refused with what was wrong with it,
// because a store of an App must not hold whatever a path held.
func TestReadKeyOfBothFormsGitHubGives(t *testing.T) {
	key, _ := keyOfTest(t)
	cases := []struct {
		name string
		file []byte
	}{
		{name: "PKCS#8", file: key.PEM()},
		{name: "PKCS#1", file: pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key.key),
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, err := ReadKey(tc.file)
			if err != nil {
				t.Fatalf("ReadKey: %v", err)
			}
			token, err := read.JWT(5107052, time.Now())
			if err != nil {
				t.Fatalf("JWT of the key that was read: %v", err)
			}
			if !strings.Contains(token, ".") {
				t.Errorf("the key that was read signed %q, want a token of three parts", token)
			}
		})
	}
}

// TestReadKeyRefusesWhatIsNotAKey: a person points the import at the wrong file.
func TestReadKeyRefusesWhatIsNotAKey(t *testing.T) {
	cases := []struct {
		name string
		file []byte
		want string
	}{
		{name: "not PEM at all", file: []byte("the key of an app\n"), want: "not in PEM"},
		{name: "a public key is not a private one", file: publicKeyOfTest(t), want: "not an RSA private key"},
		{name: "an empty file", file: nil, want: "not in PEM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadKey(tc.file)
			if err == nil {
				t.Fatal("ReadKey returned no error, want the file to be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ReadKey = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// keyOfTest is a key of a test and its public half: a pair generated here and never
// taken from the machine, because a test that signed a token with a key of GitHub
// would be a test of somebody else's account.
func keyOfTest(t *testing.T) (Key, *rsa.PublicKey) {
	t.Helper()
	key := keyOfTheTest(t)
	return Key{key: key}, &key.PublicKey
}

// publicKeyOfTest is a public key in the PEM of a file, which is what a person
// downloads when they meant to download the private one.
func publicKeyOfTest(t *testing.T) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&keyOfTheTest(t).PublicKey)
	if err != nil {
		t.Fatalf("marshal the public key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// signedWithTheKey is whether a token carries a signature the public key of the pair
// holds: the check GitHub makes, and the one a test of a JWT has to make itself,
// because a token that is well formed and signed by nobody is well formed.
func signedWithTheKey(t *testing.T, token string, pub *rsa.PublicKey) bool {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("the token %q has %d parts, want three", token, len(parts))
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Errorf("the signature of the token is not base64: %v", err)
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) == nil
}

// objectOfTest is a part of a JWT, or a body of a request, as the map it is. A part
// of a JWT is JSON in base64 without padding, which is what it is on the wire.
func objectOfTest(t *testing.T, data string, encoded bool) map[string]any {
	t.Helper()
	if encoded {
		raw, err := base64.RawURLEncoding.DecodeString(data)
		if err != nil {
			t.Fatalf("the part %q is not base64: %v", data, err)
		}
		data = string(raw)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(data), &out); err != nil {
		t.Fatalf("the JSON %q is not an object: %v", data, err)
	}
	return out
}

// keyOfTheTest is the key every test of this package signs with, made once: a key of
// 2048 bits costs a hundred milliseconds of a machine and every test that needs a
// key needs the same one.
var (
	keyMade     sync.Once
	keyOfTests  *rsa.PrivateKey
	otherMade   sync.Once
	otherOfTest *rsa.PrivateKey
)

func keyOfTheTest(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyMade.Do(func() { keyOfTests = mustGenerate(t) })
	return keyOfTests
}

func anotherKeyOfTest(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	otherMade.Do(func() { otherOfTest = mustGenerate(t) })
	return otherOfTest
}

func mustGenerate(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate a key of a test: %v", err)
	}
	return key
}

// storeOfTest is the store of a test: the key that is in it and how many times it was
// read and written. The keychain of macOS is never in a test — no test of this
// package, of an import or of a run opens the keychain of a person
// (docs/DESIGN.md §7i).
type storeOfTest struct {
	key    []byte
	reads  int
	writes int
}

// Get returns the key the test put in the store, or the error of an empty store, so
// that errors.Is says the same thing here as it says in a run.
func (s *storeOfTest) Get(service, account string) ([]byte, error) {
	s.reads++
	if s.key == nil {
		return nil, secret.ErrNotFound
	}
	return s.key, nil
}

// Set keeps the key the test is given.
func (s *storeOfTest) Set(service, account string, value []byte) error {
	s.writes++
	s.key = value
	return nil
}

// Has says whether a key is in the store without reading it, as the keychain of macOS
// can: a report asks that of a store and not of a value.
func (s *storeOfTest) Has(service, account string) (bool, error) {
	return s.key != nil, nil
}

// sourceOfTest is a Source that talks to a server of the test: the App, its
// installation and its account are answered by the handler given, and the key of the
// App is one the test generated.
func sourceOfTest(t *testing.T, handler http.HandlerFunc) *Source {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Source{
		AppID:          5107052,
		InstallationID: 165781718,
		Repo:           "naghuale/crewflow",
		BaseURL:        server.URL,
		Store:          &storeOfTest{key: Key{key: keyOfTheTest(t)}.PEM()},
		HTTP:           server.Client(),
		Now:            func() time.Time { return time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC) },
	}
}

// sourceOfTheDocumentation is a Source whose server answers as the documentation of
// the REST API of GitHub answers, of the version crewflow asks for: the answers are
// the examples of that documentation, with the numbers of the App of this project in
// them, and the asked of it is every path the API was asked at (docs/DESIGN.md §7i).
//
// The whole point of it is that a test of this package is a test of reading the
// answers GitHub gives and not of reading answers a test made up: an invented answer
// is one that keeps agreeing with the code that reads it.
func sourceOfTheDocumentation(t *testing.T) (*Source, *asked) {
	t.Helper()
	asked := &asked{}
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		asked.of(t, r)
		name, known := answerOfTheDocumentation[r.URL.Path]
		w.Header().Set("Content-Type", "application/json")
		if !known {
			w.WriteHeader(http.StatusNotFound)
			answerOfTest(t, w, map[string]any{"message": "Not Found"})
			return
		}
		writeAnswerOfTheTest(t, w, name)
	})
	return source, asked
}

// answerOfTheDocumentation is the example of the documentation that answers every
// question crewflow asks, by the path it is asked at. The installation is the answer
// of two paths — the one of the repository and the one of the number — because GitHub
// answers both with the same thing.
var answerOfTheDocumentation = map[string]string{
	appPath:                        "app.json",
	"/app/installations/165781718": "installation.json",
	"/app/installations/165781718/access_tokens": "access-token.json",
	"/repos/naghuale/crewflow/installation":      "installation.json",
	"/users/crewflow-executor[bot]":              "bot.json",
}

// writeAnswerOfTheTest is one of the answers of the documentation as it was on the
// wire: the bytes of the example, and not an object a test built out of the fields it
// happens to read.
func writeAnswerOfTheTest(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	answer, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read the answer %s of the documentation: %v", name, err)
	}
	if _, err := w.Write(answer); err != nil {
		t.Errorf("write the answer %s of the documentation: %v", name, err)
	}
}

// answerOfTest writes the answer of the server of a test.
func answerOfTest(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("write the answer of the server of the test: %v", err)
	}
}

// answerOfTheToken is the answer of the documentation to a request for a token of the
// installation of this project, as the map it is. A test that needs another answer
// changes one field of it: the shape of an answer of GitHub is the one the
// documentation writes, and a test that built an answer of its own would keep
// agreeing with the code that reads it however that code changes.
func answerOfTheToken(t *testing.T) map[string]any {
	t.Helper()
	return objectOfTheTest(t, "access-token.json")
}

// objectOfTheTest is a file of testdata as the object it holds: an example of the
// documentation of the API of GitHub, or the body of a request in the form of it.
func objectOfTheTest(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read the example %s of the documentation: %v", name, err)
	}
	return objectOfTest(t, string(data), false)
}

// printedJSON is a body as it goes on the wire: it is read as JSON and written again,
// and JSON writes the fields of an object in one order, so that two of them are the
// same document exactly when their bytes are. A map has no order of its own, and a
// test that compared two of them field by field would only say that the code agrees
// with itself.
func printedJSON(t *testing.T, body map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("write the body as JSON: %v", err)
	}
	return string(encoded)
}

// readJSON is an example of the documentation as it goes on the wire, for a test that
// compares what crewflow asked with what the documentation asks of it.
func readJSON(t *testing.T, name string) string {
	t.Helper()
	return printedJSON(t, objectOfTheTest(t, name))
}

// pathAmong is the repository of a request that was written with its owner in it, and
// the names that were written without: this is the difference the API of GitHub makes
// between 201 and 422, and the whole of what this task is about.
func pathAmong(t *testing.T, body map[string]any) (string, []string) {
	t.Helper()
	repositories, _ := body["repositories"].([]any)
	var path string
	var names []string
	for _, repository := range repositories {
		name, ok := repository.(string)
		if !ok {
			t.Errorf("the repositories of the request are %v, want names of repositories", repositories)
			continue
		}
		if strings.Contains(name, "/") {
			if path == "" {
				path = name
			}
			continue
		}
		names = append(names, name)
	}
	return path, names
}
