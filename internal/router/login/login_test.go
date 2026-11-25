package login

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	pay := base64.RawURLEncoding.EncodeToString(b)
	return hdr + "." + pay + ".sig"
}

func assertNoSecretLeak(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" && strings.Contains(msg, s) {
			t.Fatalf("error leaked secret %q: %v", s, err)
		}
	}
}

func TestClaudeFileStore(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	expMs := now.Add(time.Hour).UnixMilli()
	const tok = "fictional-claude-access-token"
	const refresh = "fictional-claude-refresh-token"
	const acct = "acct-example-1"

	writeCreds := func(t *testing.T, dir string, mode os.FileMode, body string) string {
		t.Helper()
		p := filepath.Join(dir, claudeCredentialsFile)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("valid credentials", func(t *testing.T) {
		dir := t.TempDir()
		writeCreds(t, dir, 0o600, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":%q,"expiresAt":%d}}`, tok, refresh, expMs))
		r := New(Config{
			OS:            "linux",
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "claude-main", Provider: ProviderClaude, Dir: dir, Account: acct}},
		})
		got, err := r.Login(context.Background(), "claude-main")
		if err != nil {
			t.Fatalf("Login() error = %v", err)
		}
		if got.Authorization != "Bearer "+tok || got.Account != acct || !got.Expires.Equal(time.UnixMilli(expMs)) {
			t.Fatalf("Login() = %+v", got)
		}
	})

	cases := []struct {
		name    string
		setup   func(t *testing.T, dir string)
		ownUID  func() int
		wantErr error
	}{
		{
			name:    "missing file",
			setup:   func(t *testing.T, dir string) {},
			wantErr: ErrNotFound,
		},
		{
			name: "expired token",
			setup: func(t *testing.T, dir string) {
				writeCreds(t, dir, 0o600, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, now.Add(-time.Minute).UnixMilli()))
			},
			wantErr: ErrExpired,
		},
		{
			name: "malformed json",
			setup: func(t *testing.T, dir string) {
				writeCreds(t, dir, 0o600, `{not-json`)
			},
			wantErr: ErrInvalid,
		},
		{
			name: "missing accessToken",
			setup: func(t *testing.T, dir string) {
				writeCreds(t, dir, 0o600, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"","expiresAt":%d}}`, expMs))
			},
			wantErr: ErrInvalid,
		},
		{
			name: "permissive file mode",
			setup: func(t *testing.T, dir string) {
				writeCreds(t, dir, 0o644, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, expMs))
			},
			wantErr: ErrUntrustedFile,
		},
		{
			name: "group-writable dir",
			setup: func(t *testing.T, dir string) {
				writeCreds(t, dir, 0o600, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, expMs))
				if err := os.Chmod(dir, 0o770); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrUntrustedFile,
		},
		{
			name: "symlink rejected",
			setup: func(t *testing.T, dir string) {
				target := filepath.Join(dir, "real.json")
				if err := os.WriteFile(target, []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, expMs)), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(dir, claudeCredentialsFile)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrUntrustedFile,
		},
		{
			name: "wrong owner uid",
			setup: func(t *testing.T, dir string) {
				writeCreds(t, dir, 0o600, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, expMs))
			},
			ownUID:  func() int { return os.Getuid() + 1 },
			wantErr: ErrUntrustedFile,
		},
		{
			name: "oversized file",
			setup: func(t *testing.T, dir string) {
				pad := strings.Repeat("a", maxCredentialBytes+100)
				writeCreds(t, dir, 0o600, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, pad, expMs))
			},
			wantErr: ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, dir)
			r := New(Config{
				OS:            "linux",
				Now:           func() time.Time { return now },
				OwnUID:        tc.ownUID,
				Subscriptions: []Subscription{{ID: "claude-main", Provider: ProviderClaude, Dir: dir, Account: acct}},
			})
			_, err := r.Login(context.Background(), "claude-main")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Login() error = %v, want %v", err, tc.wantErr)
			}
			assertNoSecretLeak(t, err, tok, refresh, acct)
		})
	}
}

func TestClaudeKeychainStore(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	expMs := now.Add(time.Hour).UnixMilli()
	const tok = "fictional-keychain-token"
	const acct = "acct-kc-1"

	if got := claudeKeychainService(""); got != claudeKeychainBase {
		t.Fatalf("default service = %q, want %q", got, claudeKeychainBase)
	}
	dir := t.TempDir()
	svc := claudeKeychainService(dir)
	if !strings.HasPrefix(svc, claudeKeychainBase+"-") || len(svc) != len(claudeKeychainBase)+1+8 {
		t.Fatalf("custom dir service = %q", svc)
	}

	r := New(Config{
		OS:  "darwin",
		Now: func() time.Time { return now },
		Keychain: func(_ context.Context, service string) ([]byte, error) {
			if service != svc {
				return nil, fmt.Errorf("unexpected service %q", service)
			}
			return []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, expMs)), nil
		},
		Subscriptions: []Subscription{{ID: "claude-kc", Provider: ProviderClaude, Dir: dir, Account: acct}},
	})
	got, err := r.Login(context.Background(), "claude-kc")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if got.Authorization != "Bearer "+tok || got.Account != acct {
		t.Fatalf("Login() = %+v", got)
	}

	// Keychain unavailable (locked) must surface ErrUnavailable, not ErrNotFound.
	rLocked := New(Config{
		OS:  "darwin",
		Now: func() time.Time { return now },
		Keychain: func(_ context.Context, _ string) ([]byte, error) {
			return nil, errors.New("security exited 36: " + tok)
		},
		Subscriptions: []Subscription{{ID: "claude-kc", Provider: ProviderClaude, Dir: dir}},
	})
	_, err = rLocked.Login(context.Background(), "claude-kc")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("locked keychain error = %v, want ErrUnavailable", err)
	}
	assertNoSecretLeak(t, err, tok, acct)
}

func TestCodexFileStore(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	exp := now.Add(2 * time.Hour).Unix()
	const acct = "acct_codex_example"
	const refresh = "fictional-codex-refresh"

	writeAuth := func(t *testing.T, dir string, mode os.FileMode, tok, accountID string) {
		t.Helper()
		body := fmt.Sprintf(`{"tokens":{"access_token":%q,"refresh_token":%q,"account_id":%q}}`, tok, refresh, accountID)
		p := filepath.Join(dir, codexAuthFile)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("valid auth.json with explicit account_id", func(t *testing.T) {
		dir := t.TempDir()
		jwt := makeJWT(t, map[string]any{"exp": exp})
		writeAuth(t, dir, 0o600, jwt, acct)
		if err := os.WriteFile(filepath.Join(dir, codexConfigFile), []byte("cli_auth_credentials_store = \"file\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := New(Config{
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "codex-main", Provider: ProviderCodex, Dir: dir}},
		})
		got, err := r.Login(context.Background(), "codex-main")
		if err != nil {
			t.Fatalf("Login() error = %v", err)
		}
		if got.Authorization != "Bearer "+jwt || got.Account != acct || !got.Expires.Equal(time.Unix(exp, 0)) {
			t.Fatalf("Login() = %+v", got)
		}
	})

	t.Run("account_id fallback from JWT claim", func(t *testing.T) {
		dir := t.TempDir()
		jwt := makeJWT(t, map[string]any{
			"exp":                         exp,
			"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_from_jwt"},
		})
		writeAuth(t, dir, 0o600, jwt, "")
		r := New(Config{
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "codex-main", Provider: ProviderCodex, Dir: dir}},
		})
		got, err := r.Login(context.Background(), "codex-main")
		if err != nil {
			t.Fatalf("Login() error = %v", err)
		}
		if got.Account != "acct_from_jwt" {
			t.Fatalf("Account = %q, want acct_from_jwt", got.Account)
		}
	})

	t.Run("keyring store returns ErrUnsupportedStore", func(t *testing.T) {
		dir := t.TempDir()
		jwt := makeJWT(t, map[string]any{"exp": exp})
		writeAuth(t, dir, 0o600, jwt, acct)
		if err := os.WriteFile(filepath.Join(dir, codexConfigFile), []byte("cli_auth_credentials_store = \"keyring\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := New(Config{
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "codex-main", Provider: ProviderCodex, Dir: dir}},
		})
		_, err := r.Login(context.Background(), "codex-main")
		if !errors.Is(err, ErrUnsupportedStore) {
			t.Fatalf("Login() error = %v, want ErrUnsupportedStore", err)
		}
		assertNoSecretLeak(t, err, jwt, refresh, acct)
	})

	t.Run("table-scoped cli_auth_credentials_store is ignored", func(t *testing.T) {
		dir := t.TempDir()
		jwt := makeJWT(t, map[string]any{"exp": exp})
		writeAuth(t, dir, 0o600, jwt, acct)
		cfg := "[profiles.work]\ncli_auth_credentials_store = \"keyring\"\n"
		if err := os.WriteFile(filepath.Join(dir, codexConfigFile), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		r := New(Config{
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "codex-main", Provider: ProviderCodex, Dir: dir}},
		})
		got, err := r.Login(context.Background(), "codex-main")
		if err != nil {
			t.Fatalf("Login() error = %v, want nil when cli_auth_credentials_store is only under a table header", err)
		}
		if got.Account != acct {
			t.Fatalf("Account = %q, want %q", got.Account, acct)
		}
	})

	t.Run("expired JWT returns ErrExpired", func(t *testing.T) {
		dir := t.TempDir()
		jwt := makeJWT(t, map[string]any{"exp": now.Add(-time.Second).Unix()})
		writeAuth(t, dir, 0o600, jwt, acct)
		r := New(Config{
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "codex-main", Provider: ProviderCodex, Dir: dir}},
		})
		_, err := r.Login(context.Background(), "codex-main")
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("Login() error = %v, want ErrExpired", err)
		}
		assertNoSecretLeak(t, err, jwt, refresh, acct)
	})

	t.Run("non-JWT access token returns ErrInvalid", func(t *testing.T) {
		dir := t.TempDir()
		writeAuth(t, dir, 0o600, "not-a-jwt", acct)
		r := New(Config{
			Now:           func() time.Time { return now },
			Subscriptions: []Subscription{{ID: "codex-main", Provider: ProviderCodex, Dir: dir}},
		})
		_, err := r.Login(context.Background(), "codex-main")
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("Login() error = %v, want ErrInvalid", err)
		}
		assertNoSecretLeak(t, err, "not-a-jwt", refresh, acct)
	})
}

func TestCachingConcurrencyAndUnknowns(t *testing.T) {
	var cur atomic.Int64
	cur.Store(1_900_000_000)
	var reads atomic.Int32
	var tokSeq atomic.Int32

	r := New(Config{
		OS:  "darwin",
		Now: func() time.Time { return time.Unix(cur.Load(), 0) },
		Keychain: func(_ context.Context, _ string) ([]byte, error) {
			reads.Add(1)
			n := tokSeq.Add(1)
			exp := time.Unix(cur.Load(), 0).Add(10 * time.Minute).UnixMilli()
			return []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok-%d","expiresAt":%d}}`, n, exp)), nil
		},
		Subscriptions: []Subscription{
			{ID: "sub-a", Provider: ProviderClaude, Dir: t.TempDir()},
			{ID: "sub-other", Provider: "antigravity", Dir: t.TempDir()},
		},
	})

	if _, err := r.Login(context.Background(), "missing"); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("unknown sub error = %v", err)
	}
	if _, err := r.Login(context.Background(), "sub-other"); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("unsupported provider error = %v", err)
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.Login(context.Background(), "sub-a")
			if err != nil || got.Authorization == "" {
				t.Errorf("concurrent Login() = %+v, err = %v", got, err)
			}
		}()
	}
	wg.Wait()

	// Subsequent read before expiry uses cache.
	readsBefore := reads.Load()
	if _, err := r.Login(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != readsBefore {
		t.Fatalf("expected cached hit, got %d -> %d reads", readsBefore, reads.Load())
	}

	// Advancing past expiry triggers a fresh read.
	cur.Add(11 * 60)
	if _, err := r.Login(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != readsBefore+1 {
		t.Fatalf("expected re-read after expiry, got %d -> %d reads", readsBefore, reads.Load())
	}
}
