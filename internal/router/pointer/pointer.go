// Package pointer writes, rewrites and removes the settings that send a
// Claude Code session to the account router's front
// (docs/design/router.md, "The parts" and "The pointer check"). It works on
// the text of a settings file and on files in a directory it is given;
// nothing in it knows where a real session's settings are, and nothing in
// the repository calls it yet.
//
// Three steps, in the order a pointer lives:
//
//   - Point adds the pointer: the front's address, the client secret in the
//     extra-headers variable, the first-party setting and, when given, the
//     certificate to trust.
//   - Unpoint rewrites it: the address becomes the provider's own and the
//     client secret leaves the headers variable. It changes values and
//     removes none, because a running session follows a change of these
//     settings but keeps a value whose key disappears.
//   - Remove takes the keys out. When nothing else in the file changed since
//     the pointer was written, the file gets back exactly the bytes it had
//     before.
//
// Every other byte of the file is left as the person wrote it.
package pointer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The four settings of a pointer, in the env block of a settings file.
const (
	KeyBaseURL    = "ANTHROPIC_BASE_URL"
	KeyHeaders    = "ANTHROPIC_CUSTOM_HEADERS"
	KeyFirstParty = "_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL"
	KeyTrust      = "NODE_EXTRA_CA_CERTS"
)

// SecretHeader is the header that carries the client secret. It is the same
// name the front reads and removes.
const SecretHeader = "X-Looptender-Client"

// Unpointed is what the secret's header says after Unpoint: a value that is
// no secret, in place of one.
const Unpointed = "unpointed"

// ProviderURL is the provider's own address, which Unpoint writes.
const ProviderURL = "https://api.anthropic.com"

// ErrForeign means the file already has a setting a pointer would write,
// set by someone else: another local proxy, most likely. Nothing is
// changed, and the front is never pointed at another proxy.
var ErrForeign = errors.New("the settings already send this session somewhere else")

// ErrChanged means the pointer in the file is not the one that was written:
// someone edited it since. Nothing is changed.
var ErrChanged = errors.New("the pointer in the settings is not the one that was written")

// ErrSettings means the file is not a JSON object with, if it has one, an
// object for env.
var ErrSettings = errors.New("the settings are not a JSON object")

// Pointer is what Point writes.
type Pointer struct {
	// BaseURL is the front's address.
	BaseURL string
	// Secret is the client secret.
	Secret string
	// TrustFile is the front's certificate, "" when the front speaks plain
	// HTTP.
	TrustFile string
}

// Record is what Point remembers so that Unpoint and Remove can tell their
// own pointer from anything else and put the file back as it was. It holds
// the file's earlier bytes, which may be the person's settings, and so
// belongs in a private place. It holds no secret: of the client secret only
// a digest.
type Record struct {
	// Original is the file's bytes before Point; Absent says there was no
	// file.
	Original []byte `json:"original"`
	Absent   bool   `json:"absent,omitempty"`
	// Written is a digest of the bytes last written by Point or Unpoint.
	Written string `json:"written"`
	// BaseURL and TrustFile are the values Point wrote; SecretDigest is a
	// digest of the client secret.
	BaseURL      string `json:"baseUrl"`
	TrustFile    string `json:"trustFile,omitempty"`
	SecretDigest string `json:"secretDigest"`
	// HeadersBefore is the headers variable's value before Point, when it
	// had one; HadHeaders says it had. HadEnv says the file had an env
	// block.
	HeadersBefore string `json:"headersBefore,omitempty"`
	HadHeaders    bool   `json:"hadHeaders,omitempty"`
	HadEnv        bool   `json:"hadEnv,omitempty"`
	// Unpointed says Unpoint has run.
	Unpointed bool `json:"unpointed,omitempty"`
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func quoted(s string) []byte {
	// Not json.Marshal alone: it would write < and > as escapes, and a file
	// should read as the person would have typed it.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// env finds the settings' env block: where the top-level object starts,
// where env's object starts (-1 when there is none).
func env(settings []byte) (top, at int, err error) {
	if !json.Valid(settings) {
		return 0, 0, ErrSettings
	}
	top = skipSpace(settings, 0)
	if top >= len(settings) || settings[top] != '{' {
		return 0, 0, ErrSettings
	}
	m, ok, err := find(settings, top, "env")
	if err != nil {
		return 0, 0, ErrSettings
	}
	if !ok {
		return top, -1, nil
	}
	if settings[m.valStart] != '{' {
		return 0, 0, ErrSettings
	}
	return top, m.valStart, nil
}

// value reads one string setting of the env block; ok is false when it is
// not there. A setting that is not a string is an error.
func value(settings []byte, at int, key string) (string, bool, error) {
	if at < 0 {
		return "", false, nil
	}
	m, ok, err := find(settings, at, key)
	if err != nil || !ok {
		return "", false, err
	}
	var s string
	if json.Unmarshal(settings[m.valStart:m.valEnd], &s) != nil {
		return "", false, ErrSettings
	}
	return s, true, nil
}

// setEnv sets one string setting of the env block.
func setEnv(settings []byte, key, v string) ([]byte, error) {
	_, at, err := env(settings)
	if err != nil {
		return nil, err
	}
	return set(settings, at, key, quoted(v))
}

// headerLine is the line the secret's header takes in the headers variable.
func headerLine(v string) string { return SecretHeader + ": " + v }

// withoutOurs returns the headers variable's lines that are not the
// secret's header.
func withoutOurs(v string) []string {
	var keep []string
	for _, line := range strings.Split(v, "\n") {
		name, _, _ := strings.Cut(line, ":")
		if strings.TrimSpace(line) != "" && !strings.EqualFold(strings.TrimSpace(name), SecretHeader) {
			keep = append(keep, line)
		}
	}
	return keep
}

// hasOurs reports whether the headers variable carries the secret's header.
func hasOurs(v string) bool {
	for _, line := range strings.Split(v, "\n") {
		if name, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), SecretHeader) {
			return true
		}
	}
	return false
}

// Point adds p to settings, which may be empty for a file that does not
// exist. It refuses with ErrForeign, changing nothing, when the settings
// already carry an address, the first-party setting, the secret's header, or
// (when p names a certificate) a certificate to trust: each means something
// else already directs this session, and two proxies are never chained.
// Headers the person set themselves are kept, and the secret's header is
// added beside them.
func Point(settings []byte, p Pointer) ([]byte, Record, error) {
	if p.BaseURL == "" || p.Secret == "" || strings.ContainsAny(p.Secret, "\r\n") {
		return nil, Record{}, errors.New("a pointer needs an address and a secret")
	}
	rec := Record{Original: append([]byte(nil), settings...), Absent: len(bytes.TrimSpace(settings)) == 0, BaseURL: p.BaseURL, TrustFile: p.TrustFile, SecretDigest: digest([]byte(p.Secret))}
	out := settings
	if rec.Absent {
		out = []byte("{}\n")
	}
	top, at, err := env(out)
	if err != nil {
		return nil, Record{}, err
	}
	rec.HadEnv = at >= 0
	for _, key := range []string{KeyBaseURL, KeyFirstParty, KeyTrust} {
		if _, there, err := value(out, at, key); err != nil {
			return nil, Record{}, err
		} else if there && (key != KeyTrust || p.TrustFile != "") {
			return nil, Record{}, fmt.Errorf("%w: %s is already set", ErrForeign, key)
		}
	}
	headers, had, err := value(out, at, KeyHeaders)
	if err != nil {
		return nil, Record{}, err
	}
	if had && hasOurs(headers) {
		return nil, Record{}, fmt.Errorf("%w: %s already carries %s", ErrForeign, KeyHeaders, SecretHeader)
	}
	rec.HeadersBefore, rec.HadHeaders = headers, had
	if at < 0 {
		if out, err = set(out, top, "env", []byte("{}")); err != nil {
			return nil, Record{}, ErrSettings
		}
	}
	settingsOf := [][2]string{{KeyBaseURL, p.BaseURL}, {KeyHeaders, strings.Join(append(withoutOurs(headers), headerLine(p.Secret)), "\n")}, {KeyFirstParty, "1"}}
	if p.TrustFile != "" {
		settingsOf = append(settingsOf, [2]string{KeyTrust, p.TrustFile})
	}
	for _, kv := range settingsOf {
		if out, err = setEnv(out, kv[0], kv[1]); err != nil {
			return nil, Record{}, ErrSettings
		}
	}
	rec.Written = digest(out)
	return out, rec, nil
}

// ours reports whether the address and the secret's header in settings are
// the ones rec's pointer wrote (or, after Unpoint, rewrote).
func ours(settings []byte, rec Record) (bool, error) {
	_, at, err := env(settings)
	if err != nil {
		return false, err
	}
	base, hasBase, err := value(settings, at, KeyBaseURL)
	if err != nil {
		return false, err
	}
	headers, _, err := value(settings, at, KeyHeaders)
	if err != nil {
		return false, err
	}
	secret := ""
	for _, line := range strings.Split(headers, "\n") {
		if name, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), SecretHeader) {
			secret = strings.TrimSpace(v)
		}
	}
	if rec.Unpointed {
		return hasBase && base == ProviderURL && secret == Unpointed, nil
	}
	return hasBase && base == rec.BaseURL && digest([]byte(secret)) == rec.SecretDigest, nil
}

// Unpoint rewrites the pointer rec describes: the address becomes the
// provider's own and the secret leaves the headers variable, replaced by a
// value that is none. Both are changes of a value, never removals. It
// refuses with ErrChanged when the pointer in settings is not rec's.
//
// The caller rotates the client secret in the same step (Clients.Rotate),
// so that a copy a session still sends is already worthless: UnpointFile
// does both.
func Unpoint(settings []byte, rec Record) ([]byte, Record, error) {
	if rec.Unpointed {
		if is, err := ours(settings, rec); err != nil || !is {
			return nil, rec, ErrChanged
		}
		return settings, rec, nil
	}
	if is, err := ours(settings, rec); err != nil || !is {
		return nil, rec, ErrChanged
	}
	_, at, _ := env(settings)
	headers, _, _ := value(settings, at, KeyHeaders)
	out, err := setEnv(settings, KeyBaseURL, ProviderURL)
	if err == nil {
		out, err = setEnv(out, KeyHeaders, strings.Join(append(withoutOurs(headers), headerLine(Unpointed)), "\n"))
	}
	if err != nil {
		return nil, rec, ErrSettings
	}
	rec.Unpointed, rec.Written = true, digest(out)
	return out, rec, nil
}

// Remove takes rec's pointer out of settings. When settings are exactly
// what Point or Unpoint last wrote, the result is the file's original
// bytes, and absent is true when there was no file. Otherwise the person
// has edited the file since, and only the pointer's own settings are taken
// out, each only if it still has the value the pointer gave it; everything
// else stays as it now is.
func Remove(settings []byte, rec Record) (out []byte, absent bool, err error) {
	if digest(settings) == rec.Written {
		return append([]byte(nil), rec.Original...), rec.Absent, nil
	}
	_, at, err := env(settings)
	if err != nil {
		return nil, false, err
	}
	if at < 0 {
		return settings, false, nil
	}
	out = settings
	drop := func(key string, is func(string) bool) error {
		_, at, err := env(out)
		if err != nil || at < 0 {
			return err
		}
		v, there, err := value(out, at, key)
		if err != nil || !there || !is(v) {
			return err
		}
		out, err = remove(out, at, key)
		return err
	}
	if err := drop(KeyBaseURL, func(v string) bool { return v == rec.BaseURL || v == ProviderURL }); err != nil {
		return nil, false, err
	}
	if err := drop(KeyFirstParty, func(v string) bool { return v == "1" }); err != nil {
		return nil, false, err
	}
	if rec.TrustFile != "" {
		if err := drop(KeyTrust, func(v string) bool { return v == rec.TrustFile }); err != nil {
			return nil, false, err
		}
	}
	// The headers variable loses the secret's header and keeps the rest.
	_, at, _ = env(out)
	if headers, there, err := value(out, at, KeyHeaders); err != nil {
		return nil, false, err
	} else if there {
		if rest := withoutOurs(headers); len(rest) == 0 && hasOurs(headers) {
			out, err = remove(out, at, KeyHeaders)
		} else if hasOurs(headers) {
			out, err = setEnv(out, KeyHeaders, strings.Join(rest, "\n"))
		}
		if err != nil {
			return nil, false, ErrSettings
		}
	}
	// An env block the pointer made, and that is empty again, goes too.
	if top, at, err := env(out); err == nil && at >= 0 && !rec.HadEnv {
		if list, _, err := members(out, at); err == nil && len(list) == 0 {
			if out, err = remove(out, top, "env"); err != nil {
				return nil, false, ErrSettings
			}
		}
	}
	return out, false, nil
}
