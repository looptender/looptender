package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/router/login"
)

// attachLocalAccountStatus populates LocalAuth on unchecked account cards
// from strictly regular local credential files via internal/router/login.
// It never spawns provider CLIs or macOS Keychain security(1), never alters
// Auth.State or catalog storage, and never retains secrets or identities.
func attachLocalAccountStatus(v *accounts.View, f accounts.File, env []string, now time.Time) {
	if v == nil || len(v.Cards) != len(f.Entries) {
		return
	}
	subs := make([]login.Subscription, 0, len(f.Entries))
	idxByID := make(map[string]int, len(f.Entries))
	dirByID := make(map[string]string, len(f.Entries))
	for i, e := range f.Entries {
		if v.Cards[i].Auth.State != "unchecked" {
			continue
		}
		dir, provider, ok := localAccountCredentialDir(e, env)
		if !ok {
			continue
		}
		subs = append(subs, login.Subscription{ID: e.ID, Provider: provider, Dir: dir})
		idxByID[e.ID] = i
		dirByID[e.ID] = dir
	}
	if len(subs) == 0 {
		return
	}
	// Pass OS: "linux" so internal/router/login reads regular files only and
	// never invokes macOS security(1) during read-only panel polls.
	reader := login.New(login.Config{
		OS:            "linux",
		Now:           func() time.Time { return now },
		Subscriptions: subs,
	})
	for _, sub := range subs {
		idx := idxByID[sub.ID]
		_, err := reader.Login(context.Background(), sub.ID)
		switch {
		case err == nil:
			v.Cards[idx].LocalAuth = "file_present"
		case errors.Is(err, login.ErrExpired):
			v.Cards[idx].LocalAuth = "file_expired"
		case errors.Is(err, login.ErrNotFound):
			if st, statErr := os.Lstat(dirByID[sub.ID]); statErr == nil && st.IsDir() {
				v.Cards[idx].LocalAuth = "file_missing"
			}
		}
	}
}

func localAccountCredentialDir(e accounts.Entry, env []string) (dir, provider string, ok bool) {
	switch e.Provider {
	case "codex":
		if e.Mode == "explicit_directory" {
			return e.Directory, login.ProviderCodex, e.Directory != ""
		}
		if e.Mode != "inherited" {
			return "", "", false
		}
		if h := lookupEnvSlice(env, "CODEX_HOME"); h != "" {
			return h, login.ProviderCodex, true
		}
		if home := lookupEnvSlice(env, "HOME"); home != "" {
			return filepath.Join(home, ".codex"), login.ProviderCodex, true
		}
	case "claude-code":
		// On macOS, Claude stores OAuth credentials in Keychain rather than
		// .credentials.json; only inspect regular files on non-Darwin hosts.
		if runtime.GOOS == "darwin" {
			return "", "", false
		}
		if e.Mode == "explicit_directory" {
			return e.Directory, login.ProviderClaude, e.Directory != ""
		}
		if e.Mode != "inherited" {
			return "", "", false
		}
		if c := lookupEnvSlice(env, "CLAUDE_CONFIG_DIR"); c != "" {
			return c, login.ProviderClaude, true
		}
		if home := lookupEnvSlice(env, "HOME"); home != "" {
			return filepath.Join(home, ".claude"), login.ProviderClaude, true
		}
	}
	return "", "", false
}

func lookupEnvSlice(env []string, key string) string {
	for _, pair := range env {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
