package front

import (
	"io"
	"net/http"
	"net/url"
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

func TestReplaceOnce(t *testing.T) {
	body := []byte(`{"a":"one","b":"two"}`)
	if out, ok := replaceOnce(body, "one", "ONE"); !ok || string(out) != `{"a":"ONE","b":"two"}` || string(body) != `{"a":"one","b":"two"}` {
		t.Fatalf("%s %v (the input must not be written to)", out, ok)
	}
	for name, c := range map[string][2]string{"another length": {"one", "four"}, "absent": {"six", "SIX"}, "empty": {"", ""}, "nothing to put": {"one", ""}} {
		if out, ok := replaceOnce(body, c[0], c[1]); ok || string(out) != string(body) {
			t.Errorf("%s: %s %v", name, out, ok)
		}
	}
	if _, ok := replaceOnce([]byte(`one one`), "one", "ONE"); ok {
		t.Error("replaced an id that occurs twice")
	}
}
