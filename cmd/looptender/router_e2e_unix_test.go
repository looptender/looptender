//go:build unix

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/router/channel"
	"github.com/looptender/looptender/internal/router/core"
	"github.com/looptender/looptender/internal/router/front"
	"github.com/looptender/looptender/internal/router/login"
	"github.com/looptender/looptender/internal/router/pointer"
	"github.com/looptender/looptender/internal/router/standin"
)

func makeSyntheticCodexHome(t *testing.T, accountID string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(2 * time.Hour).Unix()
	claims, err := json.Marshal(map[string]any{
		"exp": exp,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	payload, err := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"access_token": jwt,
			"account_id":   accountID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func makeCodexAccountsCatalog(t *testing.T, id, label, codexHome string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "accounts")
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	err := accounts.Update(dir, func(f *accounts.File) error {
		e := accounts.Entry{
			ID:        id,
			Label:     label,
			Provider:  login.ProviderCodex,
			Mode:      "explicit_directory",
			Directory: codexHome,
			CLI:       "/usr/local/bin/codex",
			Workdir:   workDir,
			Revision:  "fedcba9876543210fedcba9876543210",
			UpdatedAt: time.Now().UTC(),
		}
		e.Context = accounts.Context(*f, e, nil)
		f.Entries = append(f.Entries, e)
		return nil
	})
	if err != nil {
		t.Fatalf("accounts.Update: %v", err)
	}
	return dir
}

func readPointedSettingsEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return doc.Env
}

func extractClientSecret(t *testing.T, customHeaders string) string {
	t.Helper()
	for _, line := range strings.Split(customHeaders, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), pointer.SecretHeader) {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("no %s in ANTHROPIC_CUSTOM_HEADERS=%q", pointer.SecretHeader, customHeaders)
	return ""
}

func TestRouterEndToEndCLIAndStandin(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")

	routerDir := shortRouterTestDir(t, "e2e-")
	codexHome := makeSyntheticCodexHome(t, "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	accountsDir := makeCodexAccountsCatalog(t, "0123456789abcdef0123456789abcdef", "spare-codex", codexHome)

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
		rx := regexp.MustCompile(re)
		for time.Now().Before(deadline) {
			if m := rx.FindStringSubmatch(out.String()); m != nil {
				return m[len(m)-1]
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q in %q", re, out.String())
		return ""
	}

	routerOut, stopRouter, routerDone := start("router", "run", "--dir", routerDir, "--accounts", accountsDir)
	defer stopRouter()
	waitFor(routerOut, `router: answering in`)

	frontOut, stopFront, frontDone := start("router", "front", "--dir", routerDir, "--upstream", provider.URL, "--tls", "--drain", "2s")
	defer stopFront()
	frontURL := waitFor(frontOut, `front: listening on (https://127\.0\.0\.1:\d+)`)

	settingsDir := filepath.Join(t.TempDir(), "claude-settings")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	origBytes := []byte("{\n  \"theme\": \"dark\",\n  \"model\": \"sonnet\"\n}\n")
	if err := os.WriteFile(settingsPath, origBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runRouterCLI(t, "on", "--dir", routerDir, "--front-url", frontURL, "--tls", "--settings", settingsPath)
	if code != exitOK {
		t.Fatalf("router on failed: %d %s %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "router: on (1 pointed)") {
		t.Fatalf("unexpected router on output: %q", stdout)
	}

	envMap := readPointedSettingsEnv(t, settingsPath)
	if envMap[pointer.KeyBaseURL] != frontURL {
		t.Fatalf("settings %s = %q, want %q", pointer.KeyBaseURL, envMap[pointer.KeyBaseURL], frontURL)
	}
	secret := extractClientSecret(t, envMap[pointer.KeyHeaders])
	certPEM, err := os.ReadFile(envMap[pointer.KeyTrust])
	if err != nil {
		t.Fatalf("read trusted cert %q: %v", envMap[pointer.KeyTrust], err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to parse front cert PEM")
	}

	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{RootCAs: roots},
		},
		Timeout: 10 * time.Second,
	}
	const ownToken = "sk-ant-oat01-example-own-login"
	req, err := http.NewRequest(http.MethodPost, envMap[pointer.KeyBaseURL]+"/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ownToken)
	req.Header.Set(pointer.SecretHeader, secret)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxied request through front failed: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytes), "event: message") {
		t.Fatalf("unexpected response: status=%d body=%q", resp.StatusCode, string(bodyBytes))
	}
	if up.Served(ownToken) != 1 {
		t.Fatalf("expected standin upstream to serve 1 request, got %d", up.Served(ownToken))
	}
	recs := up.Records()
	if len(recs) != 1 || slices.Contains(recs[0].Headers, pointer.SecretHeader) {
		t.Fatalf("unexpected upstream records (secret header must be stripped): %+v", recs)
	}

	// Verify router status reports enabled, answering, pointed=1, hasFrontCert=true.
	code, stdout, stderr = runRouterCLI(t, "status", "--dir", routerDir, "--json")
	if code != exitOK {
		t.Fatalf("router status failed: %d %s", code, stderr)
	}
	var st routerStatusOut
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.Directory != "private" || !st.Answering || st.PointedCount != 1 || !st.HasFrontCert {
		t.Fatalf("unexpected router status: %+v", st)
	}

	// Turn router off and confirm fixture settings file is restored cleanly.
	code, stdout, stderr = runRouterCLI(t, "off", "--dir", routerDir)
	if code != exitOK || !strings.Contains(stdout, "router: off") {
		t.Fatalf("router off failed: %d %q %q", code, stdout, stderr)
	}
	restored, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(restored, origBytes) {
		t.Fatalf("settings not restored cleanly: got %q want %q (err=%v)", restored, origBytes, err)
	}

	code, stdout, _ = runRouterCLI(t, "status", "--dir", routerDir, "--json")
	if code != exitOK || json.Unmarshal([]byte(stdout), &st) != nil || st.Enabled || st.PointedCount != 0 {
		t.Fatalf("unexpected post-off status: %d %+v", code, st)
	}

	stopRouter()
	if c := <-routerDone; c != exitOK {
		t.Fatalf("router run exit %d: %s", c, routerOut.String())
	}
	stopFront()
	if c := <-frontDone; c != exitOK {
		t.Fatalf("router front exit %d: %s", c, frontOut.String())
	}
	if logs := routerOut.String() + frontOut.String(); strings.Contains(logs, ownToken) || strings.Contains(logs, secret) || strings.Contains(logs, "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee") {
		t.Fatalf("process output leaked sensitive value: %s", logs)
	}
}

func TestRouterEndToEndRoutedFailoverWithLoginReader(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")

	const spareAccount = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	const ownAccount = "99999999-2222-4333-8444-555555555555"

	routerDir := shortRouterTestDir(t, "rroute-")
	codexHome := makeSyntheticCodexHome(t, spareAccount)
	const spareEntryID = "0123456789abcdef0123456789abcdef"
	accountsDir := makeCodexAccountsCatalog(t, spareEntryID, "spare-codex", codexHome)

	cat, err := accounts.Load(accountsDir)
	if err != nil {
		t.Fatal(err)
	}
	authRaw, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsedAuth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(authRaw, &parsedAuth); err != nil {
		t.Fatal(err)
	}
	spareToken := parsedAuth.Tokens.AccessToken

	var subs []login.Subscription
	for _, entry := range cat.Entries {
		subs = append(subs, login.Subscription{
			ID:       entry.ID,
			Provider: entry.Provider,
			Dir:      entry.Directory,
		})
	}
	loginReader := login.New(login.Config{Subscriptions: subs})

	up := &standin.Upstream{}
	const ownToken = "sk-ant-oat01-example-own-limit"
	up.Script(ownToken, standin.Step{Status: http.StatusTooManyRequests, Header: map[string]string{"Retry-After": "60"}, Body: `{"error":"rate_limit"}`})
	up.Script(spareToken, standin.Step{Events: 2})
	provider := httptest.NewServer(up)
	defer provider.Close()
	upURL, _ := url.Parse(provider.URL)

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	frontURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	settingsDir := filepath.Join(t.TempDir(), "claude-settings")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	origBytes := []byte("{\n  \"theme\": \"dark\"\n}\n")
	if err := os.WriteFile(settingsPath, origBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := runRouterCLI(t, "on", "--allow-unstarted", "--dir", routerDir, "--front-url", frontURL, "--settings", settingsPath); code != exitOK {
		ln.Close()
		t.Fatalf("router on failed: %d %s", code, stderr)
	}
	ps, err := readPointerState(routerDir)
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	clientID := clientIDForPath(settingsPath)

	srv, err := channel.Listen(routerDir)
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	rt := core.New(core.Config{
		Order:  []string{"own-home", spareEntryID},
		Home:   map[string]string{clientID: "own-home"},
		Logins: loginReader,
	})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = srv.Serve(rt)
	}()
	defer func() {
		srv.Close()
		<-srvDone
	}()

	f, err := front.New(front.Config{
		Upstream: upURL,
		Port:     port,
		Client:   ps.Clients.Lookup,
	}, channel.NewClient(routerDir))
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	go f.Serve(ln)
	defer f.Stop(context.Background())

	envMap := readPointedSettingsEnv(t, settingsPath)
	secret := extractClientSecret(t, envMap[pointer.KeyHeaders])

	reqBody := `{"model":"claude-sonnet-4-6","messages":[],"metadata":{"user_id":"{\"device_id\":\"abab\",\"account_uuid\":\"` + ownAccount + `\",\"session_id\":\"11111111-2222-4333-8444-555555555555\"}"}}`
	req, err := http.NewRequest(http.MethodPost, envMap[pointer.KeyBaseURL]+"/v1/messages", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ownToken)
	req.Header.Set(pointer.SecretHeader, secret)
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("routed request failed: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytes), "event: message") {
		t.Fatalf("expected failover 200 stream, got status=%d body=%q", resp.StatusCode, string(bodyBytes))
	}
	if up.Served(ownToken) != 1 || up.Served(spareToken) != 1 {
		t.Fatalf("expected own=1 spare=1 served, got own=%d spare=%d", up.Served(ownToken), up.Served(spareToken))
	}

	if code, _, stderr := runRouterCLI(t, "off", "--dir", routerDir); code != exitOK {
		t.Fatalf("router off failed: %d %s", code, stderr)
	}
	restored, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(restored, origBytes) {
		t.Fatalf("settings not restored after routed failover: got %q want %q (err=%v)", restored, origBytes, err)
	}
}
