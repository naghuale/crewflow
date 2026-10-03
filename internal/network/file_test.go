package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
)

// TestNET001AProfileIsWrittenAsFourKeysOfItsOwn: a profile is a name and four keys of
// its own, in a table of its own, and the file that comes out is one the loader of every
// other command reads (docs/DESIGN.md §7d).
func TestNET001AProfileIsWrittenAsFourKeysOfItsOwn(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))

	if err := Add(path, "office", config.Proxy{Type: "https", Host: "proxy.example.org", Port: 3128, Credentials: "none"}); err != nil {
		t.Fatalf("add a profile: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load after a profile was added: %v", err)
	}
	profile, known := cfg.Network.Proxies["office"]
	if !known {
		t.Fatalf("the profiles of the project are %s, want office among them", Names(cfg))
	}
	if profile.Type != "https" || profile.Host != "proxy.example.org" || profile.Port != 3128 || profile.Credentials != "none" {
		t.Errorf("the profile office came out as %+v, want the four keys that were given", profile)
	}
	if !strings.Contains(read(t, path), "[network.proxies.office]") {
		t.Errorf("the file of the project does not hold the table of the profile:\n%s", read(t, path))
	}
}

// TestNET002AProfileThatIsAlreadyThereIsNotOverwritten: `add` names a profile that is
// not there yet, and `edit` is the command that changes one — a person who meant to
// change a profile should see which keys they change (docs/DESIGN.md §7d).
func TestNET002AProfileThatIsAlreadyThereIsNotOverwritten(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))
	before := read(t, path)

	err := Add(path, "home", config.Proxy{Type: "socks5", Host: "elsewhere.example", Port: 1, Credentials: "secret-store"})

	if err == nil {
		t.Fatal("adding a profile that is already there returned no error, want a refusal")
	}
	if !strings.Contains(err.Error(), "crewflow network proxy edit home") {
		t.Errorf("the refusal %q does not say what to do instead", err)
	}
	if read(t, path) != before {
		t.Errorf("the file of the project changed:\n%s", read(t, path))
	}
}

// TestNET003APortThatIsNotAPortIsNotWritten: a file with a port of zero is a file no
// command can work with, and a change that does not load is not saved at all — the whole
// of the refusal is that the loader said no (docs/DESIGN.md §7d).
func TestNET003APortThatIsNotAPortIsNotWritten(t *testing.T) {
	cases := []struct {
		port    int
		mention string
	}{
		{0, "must be a port from 1 to 65535"},
		{70000, "must be a port from 1 to 65535"},
		{-1, "must be a port from 1 to 65535"},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(strings.Join([]string{"port", strings.Repeat(" ", 0)}, "")+string(rune('0'+tc.port%10))), func(t *testing.T) {
			path, _ := withFile(t, keysOf("mode", `"direct"`))
			before := read(t, path)

			err := Add(path, "office", config.Proxy{Type: "http", Host: "proxy.example.org", Port: tc.port, Credentials: "none"})

			if err == nil {
				t.Fatalf("a profile with the port %d was written, want a refusal", tc.port)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("the refusal %q does not mention %q", err, tc.mention)
			}
			if read(t, path) != before {
				t.Errorf("the file of the project changed although the change was refused:\n%s", read(t, path))
			}
		})
	}
}

// TestNET004AnAddressWithASchemeOrALoginIsRefused: a proxy is named by its type, its
// host and its port, and a host with a scheme in it is a host of nothing. The address of
// a proxy in a file of a project would also be an address of one machine, and the whole
// point of a named profile is that it is not (docs/DESIGN.md §7d).
func TestNET004AnAddressWithASchemeOrALoginIsRefused(t *testing.T) {
	cases := []struct{ host, mention string }{
		{"http://192.0.2.10", "scheme"},
		{"proxy.example.org/path", "path"},
		{"ann@proxy.example.org", "login"},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			path, _ := withFile(t, keysOf("mode", `"direct"`))
			before := read(t, path)

			err := Add(path, "office", config.Proxy{Type: "http", Host: tc.host, Port: 3128, Credentials: "none"})

			if err == nil {
				t.Fatalf("a profile with the host %q was written, want a refusal", tc.host)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("the refusal %q does not mention %q", err, tc.mention)
			}
			if read(t, path) != before {
				t.Errorf("the file of the project changed although the change was refused:\n%s", read(t, path))
			}
		})
	}
}

// TestNET005AProfileIsNeverWrittenWithALoginAndAPassword: `http://user:pass@host` is a
// URL that programs read, and a secret of a person in the file of a project. The
// credentials of a profile live in the store of secrets under the name of the profile,
// and nothing that a command prints or saves carries them (docs/DESIGN.md §7e).
func TestNET005AProfileIsNeverWrittenWithALoginAndAPassword(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))

	err := Add(path, "office", config.Proxy{
		Type: "http", Host: "ann:s3cret@proxy.example.org", Port: 3128, Credentials: "none",
	})

	if err == nil {
		t.Fatal("a profile with a login and a password in its host was written, want a refusal")
	}
	if !strings.Contains(err.Error(), "store of secrets") {
		t.Errorf("the refusal %q does not say where the credentials belong", err)
	}
	if strings.Contains(read(t, path), "s3cret") {
		t.Errorf("the password of the profile is in the file of the project:\n%s", read(t, path))
	}
}

// TestNET006AProtocolCrewflowCannotSpeakIsRefused: only the protocols crewflow really
// supports are in the file, and a profile of a protocol outside the list is a profile
// that does not work — it is refused while it is written down, not at the first run that
// needed it (docs/DESIGN.md §7d).
func TestNET006AProtocolCrewflowCannotSpeakIsRefused(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))
	before := read(t, path)

	err := Add(path, "office", config.Proxy{Type: "socks4", Host: "proxy.example.org", Port: 1080, Credentials: "none"})

	if err == nil {
		t.Fatal("a profile of the protocol socks4 was written, want a refusal")
	}
	for _, want := range []string{"network.proxies.office.type", "socks4", "http, https, socks5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
	if read(t, path) != before {
		t.Errorf("the file of the project changed although the change was refused:\n%s", read(t, path))
	}
}

// TestNET007TheActiveProfileIsEditedAtomically: a change that does not load is not
// saved, a change that loads is saved whole, and everything else in the file — the
// comments of a person and the keys they did not name — is as it was (docs/DESIGN.md §7d).
func TestNET007TheActiveProfileIsEditedAtomically(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))

	err := Edit(path, "home", config.Proxy{Type: "https", Host: "192.0.2.10", Port: 3128, Credentials: "none"})

	if err != nil {
		t.Fatalf("edit the active profile: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load after the change: %v", err)
	}
	if cfg.Network.Proxies["home"].Port != 3128 || cfg.Network.Proxies["home"].Type != "https" {
		t.Errorf("the profile home came out as %+v, want the keys that were changed", cfg.Network.Proxies["home"])
	}
	if cfg.Network.Proxies["home"].Host != "192.0.2.10" {
		t.Errorf("the host of the profile home came out as %q, want the key that was not named left as it was",
			cfg.Network.Proxies["home"].Host)
	}
	edited := read(t, path)
	if strings.Contains(edited, "port = 1082") {
		t.Errorf("the file of the project still holds the port the edit changed:\n%s", edited)
	}
	if got := strings.Count(edited, "port = "); got != 2 {
		t.Errorf("the file of the project holds %d ports, want one per profile:\n%s", got, edited)
	}
}

// TestNET007AChangeThatDoesNotLoadIsNotSavedAtAll: the port of a profile is written
// whole or not at all, and a file cut in the middle by an interruption is a file that
// says the wrong thing about the route of every run that follows.
func TestNET007AChangeThatDoesNotLoadIsNotSavedAtAll(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))
	before := read(t, path)

	err := Edit(path, "home", config.Proxy{Type: "http", Host: "192.0.2.10", Port: 0, Credentials: "none"})

	if err == nil {
		t.Fatal("a port of 0 was written into the active profile, want a refusal")
	}
	if read(t, path) != before {
		t.Errorf("the file of the project changed although the change was refused:\n%s", read(t, path))
	}
	// The file is one whole file: nothing of it is cut in half by a change that was
	// refused, and no file of its own is left in the folder.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("the folder of the file of the project: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			t.Errorf("the folder of the file of the project holds %q, want nothing beside the file", entry.Name())
		}
	}
}

// TestNET008TheActiveProfileIsNotRemoved: a route that names a profile nobody defined is
// a file that does not load, so the active one stays until the owner has said where the
// route goes instead (docs/DESIGN.md §7d).
func TestNET008TheActiveProfileIsNotRemoved(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))
	before := read(t, path)

	err := Remove(path, "home")

	if err == nil {
		t.Fatal("the active profile was removed, want a refusal")
	}
	for _, want := range []string{"home", "crewflow network mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
	if read(t, path) != before {
		t.Errorf("the file of the project changed although the change was refused:\n%s", read(t, path))
	}
}

// TestNET009TheActiveProfileIsChangedByACommand: `use` points the route at a profile and
// changes nothing else, and `remove` then takes the other one out of the file and leaves
// everything crewflow did not have to touch as it was (docs/DESIGN.md §7d).
func TestNET009TheActiveProfileIsChangedByACommand(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))

	if err := Mode(path, "proxy"); err != nil {
		t.Fatalf("switch the mode: %v", err)
	}
	if err := Use(path, "work"); err != nil {
		t.Fatalf("use the profile work: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load: %v", err)
	}
	if cfg.Network.Mode != "proxy" || cfg.Network.ActiveProxy != "work" {
		t.Fatalf("the route came out as mode %q through %q, want proxy through work", cfg.Network.Mode, cfg.Network.ActiveProxy)
	}
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	if route.Name != "work" {
		t.Errorf("the route of a request goes through %q, want the profile the owner named", route.Name)
	}

	if err := Remove(path, "home"); err != nil {
		t.Fatalf("remove the profile home: %v", err)
	}
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load after a profile was removed: %v", err)
	}
	if _, still := cfg.Network.Proxies["home"]; still {
		t.Errorf("the profiles of the project are %s, want home out of them", Names(cfg))
	}
	if cfg.Network.ActiveProxy != "work" || cfg.Network.Mode != "proxy" {
		t.Errorf("the route came out as mode %q through %q, want the removal of another profile to change nothing",
			cfg.Network.Mode, cfg.Network.ActiveProxy)
	}
	edited := read(t, path)
	if strings.Contains(edited, "[network.proxies.home]") {
		t.Errorf("the table of the removed profile is still in the file:\n%s", edited)
	}
	if !strings.Contains(edited, "[network.proxies.work]") {
		t.Errorf("the table of the profile of the route is gone from the file:\n%s", edited)
	}
}

// TestTheCommandsChangeTheFileAndTheLoaderAgrees: whatever the commands write, the file
// that comes out is one that loads, and a refusal of a command leaves the file of the
// project exactly as it was — the two halves of "a change is saved whole or not at all".
func TestTheCommandsChangeTheFileAndTheLoaderAgrees(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))
	commands := []struct {
		what string
		do   func() error
	}{
		{"add a profile", func() error {
			return Add(path, "office", config.Proxy{Type: "http", Host: "proxy.example.org", Port: 3128, Credentials: "none"})
		}},
		{"use it", func() error { return Use(path, "office") }},
		{"switch the mode", func() error { return Mode(path, "proxy") }},
		{"edit it", func() error {
			return Edit(path, "office", config.Proxy{Type: "http", Host: "proxy.example.org", Port: 3129, Credentials: "none"})
		}},
		{"remove another", func() error { return Remove(path, "home") }},
	}
	for _, command := range commands {
		if err := command.do(); err != nil {
			t.Fatalf("%s: %v", command.what, err)
		}
		if _, err := config.Load(path); err != nil {
			t.Fatalf("after %s the file of the project does not load: %v", command.what, err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load: %v", err)
	}
	if cfg.Network.Proxies["office"].Port != 3129 || cfg.Network.ActiveProxy != "office" {
		t.Errorf("the file of the project came out as %s through %q, want the profile of 3129 through office",
			Names(cfg), cfg.Network.ActiveProxy)
	}
}

// TestTheModeFallbackIsWrittenByTheCommandAndReadByTheFile: the third mode is written by
// the command and read by the loader of the file of every other command. A file that could
// not be loaded with `mode = "fallback"` was a route nobody could switch to by hand either,
// and a command that wrote it would have left a person with the error of a load instead of
// the one of the command (docs/DESIGN.md §7d).
func TestTheModeFallbackIsWrittenByTheCommandAndReadByTheFile(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))

	if err := Mode(path, "fallback"); err != nil {
		t.Fatalf("Mode(path, fallback) returned an error: %v", err)
	}
	if !strings.Contains(read(t, path), `mode = "fallback"`) {
		t.Errorf("the mode of the project is not fallback:\n%s", read(t, path))
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load of a project in the mode fallback returned an error: %v", err)
	}
	if cfg.Network.Mode != "fallback" {
		t.Errorf("the file says the mode is %q, want %q", cfg.Network.Mode, "fallback")
	}
	if err := Mode(path, "sideways"); err == nil {
		t.Error("a mode crewflow does not do was written, want a refusal that names the modes it does")
	}
	if !strings.Contains(read(t, path), `mode = "fallback"`) {
		t.Errorf("the file of the project changed although the change was refused:\n%s", read(t, path))
	}
}

// TestAProfileThatIsNotThereCannotBeUsedOrRemoved: the refusal names the profile and the
// command, and the file of the project stays as it was.
func TestAProfileThatIsNotThereCannotBeUsedOrRemoved(t *testing.T) {
	path, _ := withFile(t, keysOf("mode", `"direct"`))
	before := read(t, path)

	for what, do := range map[string]func() error{
		"use": func() error { return Use(path, "office") },
		"edit": func() error {
			return Edit(path, "office", config.Proxy{Type: "http", Host: "proxy.example.org", Port: 3128, Credentials: "none"})
		},
	} {
		if err := do(); err == nil {
			t.Errorf("%s of a profile that is in no file returned no error, want a refusal", what)
		}
	}
	if err := Remove(path, "office"); err == nil {
		t.Error("removing a profile that is in no file returned no error, want a refusal")
	}
	if read(t, path) != before {
		t.Errorf("the file of the project changed although nothing was asked of it:\n%s", read(t, path))
	}
}

// TestTheHeaderOfATableIsFoundWithACommentAfterIt: TOML lets the header of a table be
// followed by spaces and a comment, and a file that says so is a file that loads. An editor
// that did not see such a header as a header wrote a table of its own, and every command of
// the network of the project then refused the file — a person who only wanted to switch the
// route was told «Key 'network' has already been defined» (docs/DESIGN.md §7d).
func TestTheHeaderOfATableIsFoundWithACommentAfterIt(t *testing.T) {
	cases := []struct {
		what    string
		headers [][2]string
	}{
		{"a comment right after the header of the section", [][2]string{
			{"[network]", "[network] # the route of every run of the project"}}},
		{"spaces and a comment after the header of the section", [][2]string{
			{"[network]", "[network] \t # the route  "}}},
		{"a comment with no space before it", [][2]string{
			{"[network]", "[network]# the route"}}},
		{"a comment after the header of a profile", [][2]string{
			{"[network.proxies.home]", "[network.proxies.home] # at home"}}},
		{"a comment after every header of the file", [][2]string{
			{"[network]", "[network] # the route"},
			{"[network.proxies.home]", "[network.proxies.home] # at home"},
			{"[network.proxies.work]", "[network.proxies.work] # at work"}}},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			path := withHeaders(t, tc.headers)

			if err := Use(path, "home"); err != nil {
				t.Fatalf("use the profile home in a file whose header carries a comment: %v", err)
			}
			if err := Mode(path, "proxy"); err != nil {
				t.Fatalf("switch the mode of a file whose header carries a comment: %v", err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("the file of the project does not load after the changes: %v", err)
			}
			if cfg.Network.ActiveProxy != "home" || cfg.Network.Mode != "proxy" {
				t.Errorf("the route came out as the mode %q through %q, want proxy through home",
					cfg.Network.Mode, cfg.Network.ActiveProxy)
			}
			edited := read(t, path)
			if got := strings.Count(edited, "[network]"); got != 1 {
				t.Errorf("the file of the project holds %d headers of [network], want one:\n%s", got, edited)
			}
			if got := strings.Count(edited, "[network.proxies.home]"); got != 1 {
				t.Errorf("the file of the project holds %d headers of the table of home, want one:\n%s", got, edited)
			}
			if strings.Contains(edited, "network.proxies.home.active_proxy") {
				t.Errorf("the key of the route was written into the table of a profile:\n%s", edited)
			}
		})
	}
}

// TestAHeaderIsAHeaderAndNothingElseIs: what the editor reads off a line before it decides
// that the line opens a table — a comment after a header is not part of the name, a `#` in
// a quoted name is, and an array of tables is not a table this editor writes into.
func TestAHeaderIsAHeaderAndNothingElseIs(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"[network]", "network"},
		{"  [network.proxies.home]  # at home", "network.proxies.home"},
		{`["network"] # the route`, "network"},
		{`['network']`, "network"},
		{`["a # b"]`, ""},
		{"[[network]] # an array of tables", ""},
		{"[network", ""},
		{"mode = \"direct\" # the route", ""},
		{"# [network]", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got, found := headerOf(tc.line)
			if tc.want == "" {
				if found {
					t.Errorf("headerOf(%q) = %q, found — want no header", tc.line, got)
				}
				return
			}
			if !found || got != tc.want {
				t.Errorf("headerOf(%q) = %q, %v, want %q, true", tc.line, got, found, tc.want)
			}
		})
	}
}

// read is the file of a project as it stands on disk, which is what a person edits and
// what the next command reads.
func read(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(text)
}
