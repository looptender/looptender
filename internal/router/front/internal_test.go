package front

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The front logs nothing: what the standard library would write about a
// connection goes nowhere.
func TestServerLogsNowhere(t *testing.T) {
	u, _ := url.Parse("https://provider.example.com")
	f, err := New(Config{Upstream: u}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.srv.ErrorLog == nil || f.srv.ErrorLog.Writer() != io.Discard {
		t.Fatal("the front's listener can write to standard error")
	}
}

// An environment proxy carries only an encrypted tunnel: it is never used
// for a plain http upstream, where it would see the login.
func TestProxyOnlyForAnEncryptedUpstream(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	t.Setenv("NO_PROXY", "")
	u, _ := url.Parse("https://provider.example.com")
	f, err := New(Config{Upstream: u}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy := f.cfg.Transport.(*http.Transport).Proxy
	plain, _ := http.NewRequest(http.MethodPost, "http://provider.example.com/v1/messages", nil)
	if via, err := proxy(plain); via != nil || err != nil {
		t.Fatalf("a plain http upstream would go through %v (%v)", via, err)
	}
	tls, _ := http.NewRequest(http.MethodPost, "https://provider.example.com/v1/messages", nil)
	if via, _ := proxy(tls); via == nil {
		t.Skip("the environment's proxy was read before this test set it")
	}
}

// The silence bound inside a stream is off unless it is set, and the
// profile is Claude Code's unless another is given.
func TestDefaults(t *testing.T) {
	u, _ := url.Parse("https://provider.example.com")
	f, err := New(Config{Upstream: u}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.cfg.IdleWait != 0 || f.cfg.Profile.Name != "claude-code" || f.cfg.BodyWait <= 0 {
		t.Fatalf("idle %v, profile %q, body wait %v", f.cfg.IdleWait, f.cfg.Profile.Name, f.cfg.BodyWait)
	}
}

// claudeRewrite changes the account where user_id names it and nowhere
// else, or changes nothing.
func TestClaudeRewrite(t *testing.T) {
	const own, serving, session = "99999999-2222-4333-8444-555555555555", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "11111111-2222-4333-8444-555555555555"
	// body builds a request whose user_id string is written as given, with
	// prompt as the text of its one message.
	body := func(userID, prompt string) []byte {
		return []byte(`{"model":"model-large","messages":[{"role":"user","content":"` + prompt + `"}],"metadata":{"user_id":"` + userID + `"}}`)
	}
	plain := `{\"device_id\":\"abab\",\"account_uuid\":\"` + own + `\",\"session_id\":\"` + session + `\"}`
	// The same id with its first character written as an escape: once at
	// the level of the outer string, once at the level of the inner object.
	outerEscape := strings.Replace(plain, `\"`+own, `\"\u0039`+own[1:], 1)
	innerEscape := strings.Replace(plain, `\"`+own, `\"\\u0039`+own[1:], 1)

	out, ok := claudeRewrite(body(plain, "hello"), serving)
	if !ok || strings.Contains(string(out), own) || strings.Count(string(out), serving) != 1 || len(out) != len(body(plain, "hello")) {
		t.Fatalf("plain: ok=%v %s", ok, out)
	}
	if _, _, account, _, _ := claudeBody(out); account != serving {
		t.Fatalf("after the rewrite the body names %q", account)
	}

	refused := map[string][]byte{
		"the id escaped in user_id and plain in the prompt":           body(outerEscape, "about "+own),
		"the id escaped inside the inner object, plain in the prompt": body(innerEscape, "about "+own),
		"the id escaped in user_id only":                              body(outerEscape, "hello"),
		"the id plain in user_id and in the prompt":                   body(plain, "about "+own),
		"the id only in the prompt, user_id naming none":              body(`{\"session_id\":\"`+session+`\"}`, "about "+own),
		"user_id not an object":                                       body("user_x", "hello"),
		"user_id given twice":                                         []byte(`{"model":"m","metadata":{"user_id":"` + plain + `","user_id":"` + plain + `"}}`),
		"user_id not a string":                                        []byte(`{"model":"m","metadata":{"user_id":{"account_uuid":"` + own + `"}}}`),
		"not JSON":                                                    []byte("hello " + own),
	}
	for name, b := range refused {
		if out, ok := claudeRewrite(b, serving); ok || string(out) != string(b) {
			t.Errorf("%s: rewritten to %s", name, out)
		}
	}
	// The serving id must be a UUID, and another one.
	for name, to := range map[string]string{"not a UUID": strings.Repeat("x", len(own)), "with a quote": own[:35] + `"`, "shorter": "abc", "empty": "", "the same account": own} {
		b := body(plain, "hello")
		if out, ok := claudeRewrite(b, to); ok || string(out) != string(b) {
			t.Errorf("%s: rewritten", name)
		}
	}
	// The input is never written to.
	in := body(plain, "hello")
	keep := string(in)
	claudeRewrite(in, serving)
	if string(in) != keep {
		t.Fatal("the request's own body was written to")
	}
}

// A path is judged in its plain form: no spelling of a refused path passes.
func TestPathsAreJudgedPlain(t *testing.T) {
	p := ClaudeCode()
	for _, raw := range []string{
		"/api/oauth/token", "/api/oauth/./token", "/api/oauth//token", "/api/oauth/%2e/token", "/api/oauth/x/../token", "/api/oauth/%2E%2E/oauth/token",
		"/api/OAuth/Token", "/api/oauth%2Ftoken", "/api/oauth/%252e/token", "/v1/messages/", "/v1//messages", "/v1/./messages",
		"/api/auth/trusted_devices", "/api/oauth/claude_cli/create_api_key", "/api/billing/usage", "/api/payment_methods", "/api/prepaid/credits",
		"/metrics", "/", "",
	} {
		u, err := url.Parse("http://127.0.0.1" + raw)
		if err == nil && p.forwards(u.Path) {
			t.Errorf("%q is forwarded", raw)
		}
	}
	for _, raw := range []string{"/v1/messages", "/v1/messages/count_tokens", "/api/oauth/profile", "/api/hello", "/v1/models/a%2Fb"} {
		u, _ := url.Parse("http://127.0.0.1" + raw)
		if !p.forwards(u.Path) {
			t.Errorf("%q is refused", raw)
		}
	}
}
