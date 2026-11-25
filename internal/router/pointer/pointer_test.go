package pointer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var made = Pointer{BaseURL: "https://127.0.0.1:47000", Secret: "made-up-client-secret", TrustFile: "/home/example/.local/state/looptender/router/front-cert.pem"}

// envOf reads the env block of settings as a reader of the file would.
func envOf(t *testing.T, settings []byte) map[string]string {
	t.Helper()
	var f struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(settings, &f); err != nil {
		t.Fatalf("the result is not valid settings: %v\n%s", err, settings)
	}
	return f.Env
}

// The settings a person may have, in the layouts a person may have them.
var layouts = map[string]string{
	"no file":                "",
	"an empty object":        "{}\n",
	"two-space indent":       "{\n  \"model\": \"example\",\n  \"hooks\": {\"Stop\": [{\"x\": [1, 2, {\"y\": \"} ] \\\" ,\"}]}]}\n}\n",
	"tabs":                   "{\n\t\"model\": \"example\",\n\t\"permissions\": {\n\t\t\"allow\": [\"Read\"]\n\t}\n}\n",
	"four spaces, with env":  "{\n    \"env\": {\n        \"EDITOR\": \"vi\",\n        \"EXAMPLE\": \"a, \\\"quoted\\\" } value\"\n    },\n    \"model\": \"example\"\n}\n",
	"an empty env":           "{\n  \"env\": {},\n  \"model\": \"example\"\n}\n",
	"env last":               "{\n  \"model\": \"example\",\n  \"env\": {\n    \"EDITOR\": \"vi\"\n  }\n}",
	"one line":               `{"model":"example","env":{"EDITOR":"vi"}}`,
	"one line, no env":       `{"model":"example"}`,
	"windows line ends":      "{\r\n  \"model\": \"example\",\r\n  \"env\": {\r\n    \"EDITOR\": \"vi\"\r\n  }\r\n}\r\n",
	"windows, no env":        "{\r\n  \"model\": \"example\"\r\n}\r\n",
	"own headers":            "{\n  \"env\": {\n    \"ANTHROPIC_CUSTOM_HEADERS\": \"X-Example: one\\nX-Other: two\"\n  }\n}\n",
	"own certificate bundle": "{\n  \"env\": {\n    \"NODE_EXTRA_CA_CERTS\": \"/etc/example/corporate.pem\"\n  }\n}\n",
	"unicode and escapes":    "{\n  \"note\": \"caf\\u00e9 ü \\\\ \\/ <tag>\",\n  \"env\": {\n    \"GREETING\": \"héllo\"\n  }\n}\n",
}

// Point, then Remove, gives back exactly the bytes that were there, for
// every layout; so does Point, Unpoint, Remove.
func TestPointAndRemoveRestoreTheOriginalBytes(t *testing.T) {
	for name, original := range layouts {
		t.Run(name, func(t *testing.T) {
			p := made
			if name == "own certificate bundle" {
				p.TrustFile = "" // a certificate of the person's own is in the way; see the refusals
			}
			pointed, rec, err := Point([]byte(original), p)
			if err != nil {
				t.Fatal(err)
			}
			e := envOf(t, pointed)
			if e[KeyBaseURL] != p.BaseURL || e[KeyFirstParty] != "1" || !strings.HasSuffix(e[KeyHeaders], SecretHeader+": "+p.Secret) {
				t.Fatalf("pointed env %v", e)
			}
			if p.TrustFile != "" && e[KeyTrust] != p.TrustFile {
				t.Fatalf("no trust entry: %v", e)
			}
			// What the person had is still there, value for value.
			var before struct {
				Env map[string]string `json:"env"`
			}
			json.Unmarshal([]byte(original), &before)
			for k, v := range before.Env {
				if k == KeyHeaders {
					if !strings.HasPrefix(e[k], v+"\n") {
						t.Fatalf("the person's own headers were lost: %q", e[k])
					}
				} else if e[k] != v {
					t.Fatalf("the person's %s changed from %q to %q", k, v, e[k])
				}
			}
			back, absent, err := Remove(pointed, rec)
			if err != nil || string(back) != original || absent != (original == "") {
				t.Fatalf("after Point and Remove (absent=%v, %v):\n%q\nwant\n%q", absent, err, back, original)
			}
			unpointed, rec2, err := Unpoint(pointed, rec)
			if err != nil {
				t.Fatal(err)
			}
			back, absent, err = Remove(unpointed, rec2)
			if err != nil || string(back) != original || absent != (original == "") {
				t.Fatalf("after Point, Unpoint and Remove:\n%q\nwant\n%q", back, original)
			}
		})
	}
}

// Point changes only the env block: every byte outside it stays, and inside
// it every byte the person wrote stays.
func TestPointLeavesEveryOtherByte(t *testing.T) {
	for name, original := range layouts {
		if original == "" || name == "own headers" || !strings.Contains(original, "\n") {
			continue // one-line files are covered by the round trip above
		}
		p := made
		if name == "own certificate bundle" {
			p.TrustFile = ""
		}
		pointed, _, err := Point([]byte(original), p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Take out exactly what a pointer adds, as text, and the original
		// must be left, give or take the env block Point had to make.
		text := string(pointed)
		for _, line := range strings.FieldsFunc(original, func(r rune) bool { return r == '\n' || r == '\r' }) {
			if strings.TrimSpace(line) == "" || strings.TrimSpace(line) == "}" || strings.TrimSpace(line) == "{}" || strings.Contains(line, "\"env\": {}") {
				continue
			}
			if !strings.Contains(text, strings.TrimRight(line, ",")) {
				t.Errorf("%s: the line %q is no longer in the file", name, line)
			}
		}
		if strings.Contains(original, "\r\n") && strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
			t.Errorf("%s: a bare line feed was written into a file with Windows line ends", name)
		}
		if strings.Contains(original, "\t\"model\"") && !strings.Contains(text, "\t\t\""+KeyBaseURL) {
			t.Errorf("%s: the new settings do not follow the file's tabs:\n%s", name, text)
		}
	}
}

// Unpoint changes values and removes none: the address becomes the
// provider's own, and the secret is gone from the headers, with a value
// that is no secret in its place and the person's own headers kept.
func TestUnpointRewritesAndRemovesNothing(t *testing.T) {
	pointed, rec, err := Point([]byte(layouts["own headers"]), made)
	if err != nil {
		t.Fatal(err)
	}
	unpointed, rec2, err := Unpoint(pointed, rec)
	if err != nil {
		t.Fatal(err)
	}
	e := envOf(t, unpointed)
	if e[KeyBaseURL] != ProviderURL {
		t.Fatalf("address %q", e[KeyBaseURL])
	}
	if strings.Contains(string(unpointed), made.Secret) {
		t.Fatal("the client secret is still in the file")
	}
	if e[KeyHeaders] != "X-Example: one\nX-Other: two\n"+SecretHeader+": "+Unpointed {
		t.Fatalf("headers %q", e[KeyHeaders])
	}
	for _, key := range []string{KeyBaseURL, KeyHeaders, KeyFirstParty, KeyTrust} {
		if _, there := e[key]; !there {
			t.Fatalf("%s was removed: a running session keeps a value whose key disappears", key)
		}
	}
	if !rec2.Unpointed || rec2.Written == rec.Written {
		t.Fatalf("record %+v", rec2)
	}
	// A second Unpoint changes nothing.
	again, _, err := Unpoint(unpointed, rec2)
	if err != nil || string(again) != string(unpointed) {
		t.Fatalf("a second Unpoint: %v", err)
	}
	// The record holds no secret.
	if b, _ := json.Marshal(rec2); strings.Contains(string(b), made.Secret) {
		t.Fatal("the record holds the client secret")
	}
}

// A file that already sends the session somewhere, or already has the
// settings a pointer would write, is left alone.
func TestPointRefusesWhatSomeoneElseSet(t *testing.T) {
	for name, settings := range map[string]string{
		"another address":         `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8080"}}`,
		"the first-party setting": `{"env":{"_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL":"1"}}`,
		"a certificate bundle":    layouts["own certificate bundle"],
		"our own header already":  `{"env":{"ANTHROPIC_CUSTOM_HEADERS":"x-looptender-client: something"}}`,
		"a pointer already":       "",
	} {
		t.Run(name, func(t *testing.T) {
			if name == "a pointer already" {
				b, _, _ := Point([]byte("{}"), made)
				settings = string(b)
			}
			out, _, err := Point([]byte(settings), made)
			if !errors.Is(err, ErrForeign) || out != nil {
				t.Fatalf("%v, wrote %q", err, out)
			}
		})
	}
	for name, settings := range map[string]string{
		"not JSON":               `{"env": `,
		"not an object":          `["env"]`,
		"env not an object":      `{"env": "ANTHROPIC_BASE_URL"}`,
		"a setting not a string": `{"env": {"ANTHROPIC_BASE_URL": 5}}`,
		"env given twice":        `{"env": {}, "env": {}}`,
	} {
		if out, _, err := Point([]byte(settings), made); err == nil || out != nil {
			t.Errorf("%s: %v, wrote %q", name, err, out)
		}
	}
	if _, _, err := Point([]byte("{}"), Pointer{BaseURL: "https://127.0.0.1:1", Secret: "two\nlines"}); err == nil {
		t.Error("a secret with a line break was written")
	}
}

// A pointer someone edited is not Unpoint's to rewrite.
func TestUnpointRefusesAPointerThatIsNotItsOwn(t *testing.T) {
	pointed, rec, _ := Point([]byte("{}\n"), made)
	_, at, _ := env(pointed)
	removed, _ := remove(pointed, at, KeyBaseURL)
	for name, edited := range map[string]string{
		"the address was changed": strings.Replace(string(pointed), made.BaseURL, "http://127.0.0.1:8080", 1),
		"the secret was changed":  strings.Replace(string(pointed), made.Secret, "another-secret", 1),
		"the address was removed": string(removed),
	} {
		if out, _, err := Unpoint([]byte(edited), rec); !errors.Is(err, ErrChanged) || out != nil {
			t.Errorf("%s: %v, wrote %q", name, err, out)
		}
	}
}

// When the person edited the file after the pointer was written, Remove
// takes out the pointer's own settings and keeps the edit.
func TestRemoveAfterThePersonEditedTheFile(t *testing.T) {
	original := layouts["four spaces, with env"]
	pointed, rec, _ := Point([]byte(original), made)
	edited := strings.Replace(string(pointed), `"model": "example"`, `"model": "another"`, 1)
	out, absent, err := Remove([]byte(edited), rec)
	want := strings.Replace(original, `"model": "example"`, `"model": "another"`, 1)
	if err != nil || absent || string(out) != want {
		t.Fatalf("%v\n%q\nwant\n%q", err, out, want)
	}
	// The same after Unpoint.
	unpointed, rec2, _ := Unpoint(pointed, rec)
	edited = strings.Replace(string(unpointed), `"model": "example"`, `"model": "another"`, 1)
	if out, _, err := Remove([]byte(edited), rec2); err != nil || string(out) != want {
		t.Fatalf("after Unpoint: %v\n%q", err, out)
	}
	// An env block the pointer made goes when it is empty again; one the
	// person had stays, and so does a header of the person's own.
	pointed, rec, _ = Point([]byte(layouts["two-space indent"]), made)
	edited = strings.Replace(string(pointed), `"model": "example"`, `"model": "another"`, 1)
	if out, _, _ := Remove([]byte(edited), rec); strings.Contains(string(out), `"env"`) || string(out) != strings.Replace(layouts["two-space indent"], `"model": "example"`, `"model": "another"`, 1) {
		t.Fatalf("an env block made by the pointer was left:\n%s", out)
	}
	pointed, rec, _ = Point([]byte(layouts["own headers"]), made)
	edited = strings.Replace(string(pointed), "X-Example: one", "X-Example: changed", 1)
	out, _, _ = Remove([]byte(edited), rec)
	if e := envOf(t, out); e[KeyHeaders] != "X-Example: changed\nX-Other: two" || e[KeyBaseURL] != "" {
		t.Fatalf("env %v", e)
	}
	// A setting the person changed to something of their own is theirs now.
	pointed, rec, _ = Point([]byte("{}\n"), made)
	edited = strings.Replace(string(pointed), made.BaseURL, "http://127.0.0.1:8080", 1)
	out, _, _ = Remove([]byte(edited), rec)
	if e := envOf(t, out); e[KeyBaseURL] != "http://127.0.0.1:8080" {
		t.Fatalf("an address the person set was removed: %v", e)
	}
}

func TestClients(t *testing.T) {
	var c Clients
	first, err := c.Issue("client-one")
	if err != nil || len(first) < 40 {
		t.Fatalf("%q %v", first, err)
	}
	other, _ := c.Issue("client-two")
	if id, ok := c.Lookup(first); !ok || id != "client-one" {
		t.Fatalf("lookup %q %v", id, ok)
	}
	if id, ok := c.Lookup(other); !ok || id != "client-two" {
		t.Fatalf("lookup %q %v", id, ok)
	}
	for _, s := range []string{"", "not-a-secret", first[:len(first)-1]} {
		if _, ok := c.Lookup(s); ok {
			t.Errorf("%q names a client", s)
		}
	}
	// Rotated: the old secret names nobody, at once; the other client's
	// still works.
	if err := c.Rotate("client-one"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup(first); ok {
		t.Fatal("a rotated secret still names its client")
	}
	if _, ok := c.Lookup(other); !ok {
		t.Fatal("rotating one client's secret broke another's")
	}
	// What is kept is a digest, never a secret.
	if b, _ := json.Marshal(&c); strings.Contains(string(b), first) || strings.Contains(string(b), other) {
		t.Fatalf("the secrets are kept: %s", b)
	}
}

// The three steps on a file in a test directory: pointed with the file
// closed to everyone else; un-pointed with the secret gone from the file and
// worthless at the front in the same step; removed with the original bytes
// and mode back.
func TestFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes and directory permissions are checked as on Unix")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := layouts["four spaces, with env"]
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var clients Clients
	secret, _ := clients.Issue("client-one")
	p := Pointer{BaseURL: made.BaseURL, Secret: secret, TrustFile: made.TrustFile}
	mode := Mode(path)
	rec, err := PointFile(path, p)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("a file that carries the secret has mode %v", st.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("%d files in the directory after the write", len(entries))
	}
	if _, err := PointFile(path, p); !errors.Is(err, ErrForeign) {
		t.Fatalf("pointing twice: %v", err)
	}
	rec, err = UnpointFile(path, rec, &clients, "client-one")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), secret) || envOf(t, b)[KeyBaseURL] != ProviderURL {
		t.Fatalf("after un-pointing:\n%s", b)
	}
	if _, ok := clients.Lookup(secret); ok {
		t.Fatal("the secret still names its client after un-pointing")
	}
	if err := RemoveFile(path, rec, mode); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if st, _ := os.Stat(path); string(b) != original || st.Mode().Perm() != 0o644 {
		t.Fatalf("after removal: mode %v\n%q", st.Mode(), b)
	}

	// A file the pointer made is removed again.
	fresh := filepath.Join(dir, "fresh", "settings.json")
	os.Mkdir(filepath.Dir(fresh), 0o700)
	rec, err = PointFile(fresh, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveFile(fresh, rec, Mode(fresh)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatal("a file the pointer made was left behind")
	}
	if err := RemoveFile(fresh, rec, 0); err != nil {
		t.Fatalf("removing from a file that is gone: %v", err)
	}
	if _, err := UnpointFile(fresh, rec, &clients, "client-one"); !errors.Is(err, ErrChanged) {
		t.Fatalf("un-pointing a file that is gone: %v", err)
	}
}

// A pointer carries a secret: it is not written through a link, into
// something that is not a file, or into a directory someone else can write.
func TestFilesThatCannotBeTrusted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links and directory permissions are checked as on Unix")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.json")
	os.WriteFile(target, []byte("{}\n"), 0o600)
	link := filepath.Join(dir, "settings.json")
	os.Symlink(target, link)
	if _, err := PointFile(link, made); !errors.Is(err, ErrFile) {
		t.Fatalf("through a link: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "{}\n" {
		t.Fatalf("the file behind the link was written: %q", b)
	}
	if _, err := PointFile(dir, made); !errors.Is(err, ErrFile) {
		t.Fatalf("a directory as the file: %v", err)
	}
	shared := filepath.Join(dir, "shared")
	os.Mkdir(shared, 0o777)
	os.Chmod(shared, 0o777)
	if _, err := PointFile(filepath.Join(shared, "settings.json"), made); !errors.Is(err, ErrFile) {
		t.Fatalf("in a directory others can write: %v", err)
	}
	if entries, _ := os.ReadDir(shared); len(entries) != 0 {
		t.Fatal("something was written there")
	}
	if _, err := PointFile(filepath.Join(dir, "missing", "settings.json"), made); !errors.Is(err, ErrFile) {
		t.Fatalf("in a directory that is not there: %v", err)
	}
}

// A write that cannot be completed leaves the file as it was.
func TestWriteThatFailsLeavesTheFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte("{}\n"), 0o600)
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	if _, err := PointFile(path, made); err == nil {
		t.Fatal("a write into a directory that cannot be written succeeded")
	}
	if b, _ := os.ReadFile(path); string(b) != "{}\n" {
		t.Fatalf("the file changed: %q", b)
	}
}

func TestEdit(t *testing.T) {
	for _, bad := range []string{`{"a": }`, `{"a" 1}`, `{"a": 1 "b": 2}`, `{a: 1}`, `{"a": "unterminated}`, `[1]`, ``} {
		if _, _, err := members([]byte(bad), 0); err == nil {
			t.Errorf("%q was read as an object", bad)
		}
	}
	b := []byte(`{"a": 1, "b": {"c": [1, "}"]}, "d": "x\"y"}`)
	list, end, err := members(b, 0)
	if err != nil || len(list) != 3 || end != len(b)-1 || string(b[list[1].valStart:list[1].valEnd]) != `{"c": [1, "}"]}` || string(b[list[2].valStart:list[2].valEnd]) != `"x\"y"` {
		t.Fatalf("%+v %d %v", list, end, err)
	}
	for key, want := range map[string]string{
		"a":    `{"b": {"c": [1, "}"]}, "d": "x\"y"}`,
		"b":    `{"a": 1, "d": "x\"y"}`,
		"d":    `{"a": 1, "b": {"c": [1, "}"]}}`,
		"none": string(b),
	} {
		if out, err := remove(b, 0, key); err != nil || string(out) != want {
			t.Errorf("remove %s: %q %v", key, out, err)
		}
	}
	if out, _ := remove([]byte(`{ "only": 1 }`), 0, "only"); string(out) != `{}` {
		t.Errorf("removing the only member: %q", out)
	}
	if out, _ := set(b, 0, "a", []byte("2")); string(out) != `{"a": 2, "b": {"c": [1, "}"]}, "d": "x\"y"}` {
		t.Errorf("set existing: %q", out)
	}
	if out, _ := set(b, 0, "e", []byte("true")); string(out) != `{"a": 1, "b": {"c": [1, "}"]}, "d": "x\"y", "e": true}` {
		t.Errorf("set new: %q", out)
	}
	if _, _, err := find([]byte(`{"a": 1, "a": 2}`), 0, "a"); err == nil {
		t.Error("a key given twice was accepted")
	}
}
