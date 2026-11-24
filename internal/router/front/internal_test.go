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
