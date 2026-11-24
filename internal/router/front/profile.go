package front

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// Profile is what the front knows of one provider: which paths it forwards
// and may route, where a request names its conversation and its account,
// and which answer headers tell of limits. Everything else in the front is
// the same for every provider.
type Profile struct {
	Name string
	// Forwarded are the path prefixes forwarded at all; anything else is
	// refused.
	Forwarded []string
	// Refused are lower-case parts of a path that is never forwarded: the
	// paths that mint a credential, and billing. A path is refused for what
	// it does, not for a prefix it shares with harmless calls.
	Refused []string
	// Routed are the POST paths another subscription's login may be
	// attached to. Every other forwarded path goes on unchanged.
	Routed map[string]bool
	// ConversationHeader names the conversation, when the client sends it.
	ConversationHeader string
	// AccountHeader, when set, names the account in every request and is
	// changed with the login.
	AccountHeader string
	// DropWithLogin are headers removed when another login is attached.
	DropWithLogin []string
	// LimitPrefixes are the lower-case prefixes of the answer headers that
	// are reported to the router.
	LimitPrefixes []string
	// Body takes from a request body the model name and, where the body
	// carries it, the conversation. It keeps nothing else.
	Body func(body []byte) (model, conversation string)
	// Rewrite, when set, makes a body name the serving account instead of
	// its own, by replacing one value of the same length in place. ok is
	// false, and the request is then not routed, whenever that cannot be
	// done and checked.
	Rewrite func(body []byte, to string) (out []byte, ok bool)
}

// accountID is the form of an account id the front will put into a request:
// a UUID and nothing else.
var accountID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ClaudeCode is the profile for Claude Code, as its requests were seen
// against a stand-in and the provider (docs/design/router.md, "What must be
// verified"): the conversation in a header, the account inside
// metadata.user_id, limits in anthropic-ratelimit-unified-* headers.
func ClaudeCode() Profile {
	return Profile{
		Name:               "claude-code",
		Forwarded:          []string{"/v1/", "/api/"},
		Refused:            []string{"create_api_key", "/oauth/token", "/oauth/authorize", "/auth/trusted_devices", "billing", "payment", "prepaid"},
		Routed:             map[string]bool{"/v1/messages": true, "/v1/messages/count_tokens": true},
		ConversationHeader: "X-Claude-Code-Session-Id",
		DropWithLogin:      []string{"X-Api-Key"},
		LimitPrefixes:      []string{"anthropic-ratelimit-"},
		Body:               func(body []byte) (string, string) { m, c, _, _, _ := claudeBody(body); return m, c },
		Rewrite:            claudeRewrite,
	}
}

// Codex is the profile for the Codex CLI pointed by a custom provider entry
// that keeps the ChatGPT sign-in and has WebSocket off, so the model request
// is a POST. It is written from one stand-in session and nothing uses it:
// no request with another account's login has been tried.
func Codex() Profile {
	return Profile{
		Name:               "codex",
		Forwarded:          []string{"/responses", "/models"},
		Routed:             map[string]bool{"/responses": true},
		ConversationHeader: "Session-Id",
		AccountHeader:      "Chatgpt-Account-Id",
		LimitPrefixes:      []string{"x-codex-"},
		Body:               func(body []byte) (string, string) { return topString(body, "model"), "" },
	}
}

// forwards reports whether a request path is forwarded at all. decoded is
// the path with its escapes read. A path is judged in its plain form: one
// that changes when cleaned (a dot segment, a doubled or trailing slash, in
// whatever spelling), or that still holds an escape after decoding, is
// refused, so no spelling of a refused path can pass as another.
func (p Profile) forwards(decoded string) bool {
	if decoded == "" || path.Clean(decoded) != decoded || strings.Contains(decoded, "%") {
		return false
	}
	lower := strings.ToLower(decoded)
	for _, part := range p.Refused {
		if strings.Contains(lower, part) {
			return false
		}
	}
	for _, prefix := range p.Forwarded {
		if decoded == prefix || strings.HasPrefix(decoded, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func (p Profile) limit(header string) bool {
	for _, prefix := range p.LimitPrefixes {
		if strings.HasPrefix(header, prefix) {
			return true
		}
	}
	return false
}

// topString returns the string value of one top-level key of a JSON object,
// reading past everything else.
func topString(body []byte, key string) string {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return ""
		}
		if t == key {
			var s string
			if dec.Decode(&s) != nil {
				return ""
			}
			return s
		}
		if skip(dec) != nil {
			return ""
		}
	}
	return ""
}

// claudeBody reads a Claude Code request: the model, and from
// metadata.user_id, which is a JSON object inside a string, the session and
// the account. from and to are where the user_id value lies in the body,
// from the end of its key to the end of the value; both are zero when there
// is none.
func claudeBody(body []byte) (model, conversation, account string, from, to int64) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", "", "", 0, 0
	}
	var user string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			break
		}
		switch t {
		case "model":
			if dec.Decode(&model) != nil {
				return "", "", "", 0, 0
			}
		case "metadata":
			var ok bool
			if user, from, to, ok = claudeUserID(dec); !ok {
				return model, "", "", 0, 0
			}
		default:
			if skip(dec) != nil {
				return model, "", "", 0, 0
			}
		}
	}
	var id struct {
		Account string `json:"account_uuid"`
		Session string `json:"session_id"`
	}
	if json.Unmarshal([]byte(user), &id) != nil {
		return model, "", "", 0, 0
	}
	return model, id.Session, id.Account, from, to
}

// claudeUserID reads the metadata object the decoder stands before and
// returns its user_id string with where its value lies in the input.
func claudeUserID(dec *json.Decoder) (user string, from, to int64, ok bool) {
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", 0, 0, false
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return "", 0, 0, false
		}
		if t != "user_id" {
			if skip(dec) != nil {
				return "", 0, 0, false
			}
			continue
		}
		start := dec.InputOffset()
		v, err := dec.Token()
		s, isString := v.(string)
		if err != nil || !isString || user != "" {
			return "", 0, 0, false // not a string, or user_id given twice
		}
		user, from, to = s, start, dec.InputOffset()
	}
	if _, err := dec.Token(); err != nil {
		return "", 0, 0, false
	}
	return user, from, to, user != ""
}

// claudeRewrite makes a Claude Code request name the account to instead of
// its own. It replaces the account id where it is written inside the
// user_id value, and nowhere else, with one of the same length; every other
// byte stays. It refuses, changing nothing, unless all of this holds:
//
//   - both ids are UUIDs;
//   - the request's own id occurs exactly once in the whole body, written
//     plainly, and that occurrence lies inside the user_id value (an id
//     written with an escape there, while the same id stands in a prompt,
//     must never lead to the prompt's being replaced);
//   - read again after the change, the body names the account to, and the
//     same model and conversation as before.
func claudeRewrite(body []byte, to string) ([]byte, bool) {
	model, conversation, own, from, end := claudeBody(body)
	if !accountID.MatchString(own) || !accountID.MatchString(to) || own == to {
		return body, false
	}
	at := int64(bytes.Index(body, []byte(own)))
	if bytes.Count(body, []byte(own)) != 1 || at < from || at+int64(len(own)) > end {
		return body, false
	}
	out := make([]byte, len(body))
	copy(out, body)
	copy(out[at:], to)
	if m, c, a, _, _ := claudeBody(out); m != model || c != conversation || a != to || bytes.Contains(out, []byte(own)) {
		return body, false
	}
	return out, true
}
