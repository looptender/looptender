package standin

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A record describes a credential and never holds it, or any header value.
func TestRecordHoldsNoValues(t *testing.T) {
	const login = "sk-ant-oat01-made-up-login-value"
	up := &Upstream{}
	up.Script(login, Step{Status: 429, Body: "limit"}, Step{Events: 2})
	srv := httptest.NewServer(up)
	defer srv.Close()
	for i, want := range []int{429, 200, 200} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader("a private prompt"))
		req.Header.Set("Authorization", "Bearer "+login)
		req.Header.Set("X-Private-Header", "a private value")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("request %d: status %d, want %d (the last step repeats)", i, resp.StatusCode, want)
		}
	}
	if up.Served(login) != 3 {
		t.Fatalf("served %d", up.Served(login))
	}
	rec := up.Records()[0]
	if c := rec.Credential; c.Carrier != "authorization" || c.Scheme != "bearer" || c.Kind != "oauth_access" || c.PrefixLen != 13 || c.Len != len(login) {
		t.Fatalf("shape %+v", c)
	}
	everything := fmt.Sprintf("%+v %s %+v %+v", up.Records(), rec, up.scripts, up.served)
	for _, private := range []string{login, "made-up-login", "a private value", "a private prompt"} {
		if strings.Contains(everything, private) {
			t.Fatalf("the stand-in kept %q", private)
		}
	}
	if !strings.Contains(rec.String(), "X-Private-Header") || rec.BodyBytes != int64(len("a private prompt")) {
		t.Fatalf("record %s", rec)
	}
}

func TestShapeOf(t *testing.T) {
	for _, c := range []struct {
		name, header, value string
		want                Shape
	}{
		{"none", "", "", Shape{}},
		{"an API key", "X-Api-Key", "sk-ant-api03-abcdef", Shape{Carrier: "x-api-key", Kind: "api_key", PrefixLen: 13, Len: 19}},
		{"another bearer", "Authorization", "Bearer something-else", Shape{Carrier: "authorization", Scheme: "bearer", Kind: "other", Len: 14}},
		{"no scheme", "Authorization", "opaque", Shape{Carrier: "authorization", Kind: "other", Len: 6}},
	} {
		h := http.Header{}
		if c.header != "" {
			h.Set(c.header, c.value)
		}
		if got := ShapeOf(h); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
