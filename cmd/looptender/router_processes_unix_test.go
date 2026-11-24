//go:build unix

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/router/standin"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The two processes started by hand: the front listens, asks the router
// over the private channel, and forwards. With no subscription and no
// client known, a request goes on unchanged on the login it came with;
// with the router stopped, it still does.
func TestRouterProcessesByHand(t *testing.T) {
	dir, err := os.MkdirTemp("", "rp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	up := &standin.Upstream{}
	provider := httptest.NewServer(up)
	defer provider.Close()

	start := func(args ...string) (*lockedBuffer, context.CancelFunc, chan int) {
		out := &lockedBuffer{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() { done <- run(ctx, args, out, out) }()
		return out, cancel, done
	}
	waitFor := func(out *lockedBuffer, re string) string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if m := regexp.MustCompile(re).FindStringSubmatch(out.String()); m != nil {
				return m[len(m)-1]
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("no %q in %q", re, out.String())
		return ""
	}
	routerOut, stopRouter, routerDone := start("__router-run", "--dir", dir)
	waitFor(routerOut, `router: answering in`)
	frontOut, stopFront, frontDone := start("__router-front", "--dir", dir, "--upstream", provider.URL, "--tls", "--drain", "2s")
	addr := waitFor(frontOut, `front: listening on (https://127\.0\.0\.1:\d+)`)

	certPEM, err := os.ReadFile(filepath.Join(dir, routerFrontCertName))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, routerFrontCertName)); st.Mode().Perm() != 0o600 {
		t.Fatalf("certificate mode %v", st.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("the directory holds %d files: the socket and the certificate are all there should be (no key)", len(entries))
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots}}, Timeout: 10 * time.Second}
	send := func() int {
		req, _ := http.NewRequest(http.MethodPost, addr+"/v1/messages", strings.NewReader(`{"model":"model-large"}`))
		req.Header.Set("Authorization", "Bearer sk-ant-oat01-made-up-own-login")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := send(); code != 200 || up.Served("sk-ant-oat01-made-up-own-login") != 1 {
		t.Fatalf("status %d, own login served %d", code, up.Served("sk-ant-oat01-made-up-own-login"))
	}
	stopRouter()
	if code := <-routerDone; code != exitOK {
		t.Fatalf("router exit %d: %s", code, routerOut.String())
	}
	if code := send(); code != 200 || up.Served("sk-ant-oat01-made-up-own-login") != 2 {
		t.Fatalf("with the router stopped: status %d", code)
	}
	stopFront()
	if code := <-frontDone; code != exitOK || !strings.Contains(frontOut.String(), "front: stopped; waited for 0 open requests, cut 0") || !strings.Contains(frontOut.String(), "handshakes 2, failed 0") {
		t.Fatalf("front exit %d: %s", code, frontOut.String())
	}
	// Neither says anything of a request.
	if all := routerOut.String() + frontOut.String(); strings.Contains(all, "made-up") || strings.Contains(all, "/v1/messages") {
		t.Fatalf("a process printed something of a request: %s", all)
	}
}

// What each refuses.
func TestRouterProcessesRefusals(t *testing.T) {
	open := t.TempDir()
	os.Chmod(open, 0o755)
	for name, args := range map[string][]string{
		"no directory":           {"__router-run"},
		"a directory not closed": {"__router-run", "--dir", open},
		"front, no directory":    {"__router-front"},
		"front, an ftp upstream": {"__router-front", "--dir", open, "--upstream", "ftp://example.com"},
		"front, extra argument":  {"__router-front", "--dir", open, "extra"},
	} {
		var out bytes.Buffer
		if code := run(context.Background(), args, &out, &out); code == exitOK {
			t.Errorf("%s: exit 0: %s", name, out.String())
		}
	}
}

// The certificate is written into a directory that is this user's alone,
// and a link planted under its name is removed, not followed.
func TestFrontCertificateIsNotWrittenThroughALink(t *testing.T) {
	dir, err := os.MkdirTemp("", "rc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	outside := filepath.Join(t.TempDir(), "someone-elses-file")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, routerFrontCertName)); err != nil {
		t.Fatal(err)
	}
	if err := writeFrontCertificate(dir, []byte("made-up certificate")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "untouched" {
		t.Fatalf("the file behind the planted link was written: %q", b)
	}
	st, err := os.Lstat(filepath.Join(dir, routerFrontCertName))
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Fatalf("the certificate file: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, routerFrontCertName)); string(b) != "made-up certificate" {
		t.Fatalf("content %q", b)
	}
	// A directory others can enter is refused, and nothing is written.
	open := t.TempDir()
	os.Chmod(open, 0o755)
	if err := writeFrontCertificate(open, []byte("x")); err == nil {
		t.Fatal("written into a directory that is not closed")
	}
	if entries, _ := os.ReadDir(open); len(entries) != 0 {
		t.Fatal("something was written there")
	}
}
