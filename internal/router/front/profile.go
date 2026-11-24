package front

import (
	"bytes"
	"encoding/json"
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
	// carries them, the conversation and the account it names. It keeps
	// nothing else.
	Body func(body []byte) (model, conversation, account string)
	// RewriteBody says the account a body names must become the serving
	// one. A request is then routed only when that can be done by replacing
	// one value of the same length in place.
	RewriteBody bool
}

// ClaudeCode is the profile for Claude Code, as its requests were seen
// against a stand-in and the provider (docs/design/router.md, "What must be
// verified"): the conversation in a header, the account inside
// metadata.user_id, limits in anthropic-ratelimit-unified-* headers.
func ClaudeCode() Profile {
	return Profile{
		Name:               "claude-code",
		Forwarded:          []string{"/v1/", "/api/"},
		Refused:            []string{"create_api_key", "/oauth/token", "/oauth/authorize", "billing", "payment", "prepaid"},
		Routed:             map[string]bool{"/v1/messages": true, "/v1/messages/count_tokens": true},
		ConversationHeader: "X-Claude-Code-Session-Id",
		DropWithLogin:      []string{"X-Api-Key"},
		LimitPrefixes:      []string{"anthropic-ratelimit-"},
		Body:               claudeBody,
		RewriteBody:        true,
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
		Body:               func(body []byte) (string, string, string) { return topString(body, "model"), "", "" },
	}
}

func (p Profile) forwards(path string) bool {
	ok := false
	for _, prefix := range p.Forwarded {
		if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			ok = true
		}
	}
	lower := strings.ToLower(path)
	for _, part := range p.Refused {
		if strings.Contains(lower, part) {
			return false
		}
	}
	return ok
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
// the account.
func claudeBody(body []byte) (model, conversation, account string) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", "", ""
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
				return "", "", ""
			}
		case "metadata":
			var m struct {
				UserID string `json:"user_id"`
			}
			if dec.Decode(&m) != nil {
				return model, "", ""
			}
			user = m.UserID
		default:
			if skip(dec) != nil {
				return model, "", ""
			}
		}
	}
	var id struct {
		Account string `json:"account_uuid"`
		Session string `json:"session_id"`
	}
	if json.Unmarshal([]byte(user), &id) != nil {
		return model, "", ""
	}
	return model, id.Session, id.Account
}

// replaceOnce puts another account id in place of the one a body names. It
// succeeds only when the id occurs exactly once in the whole body and the
// other has the same length; every other byte is then identical. The body
// is never decoded and written out again.
func replaceOnce(body []byte, from, to string) ([]byte, bool) {
	if from == "" || to == "" || len(from) != len(to) || bytes.Count(body, []byte(from)) != 1 {
		return body, false
	}
	return bytes.Replace(body, []byte(from), []byte(to), 1), true
}
