package login

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/router/core"
)

const (
	claudeCredentialsFile  = ".credentials.json"
	claudeKeychainBase     = "Claude Code-credentials"
	defaultKeychainTimeout = 5 * time.Second
	keychainExitNotFound   = 44
)

// claudePayload decodes only the access token and expiry; refresh tokens are ignored.
type claudePayload struct {
	ClaudeAiOauth *struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

func (r *Reader) readClaude(ctx context.Context, sub Subscription) (core.Login, error) {
	var (
		raw []byte
		err error
	)
	if r.osName == "darwin" {
		kc := r.cfg.Keychain
		if kc == nil {
			kc = readMacKeychain
		}
		raw, err = kc(ctx, claudeKeychainService(sub.Dir))
	} else {
		if sub.Dir == "" {
			return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "missing directory"}
		}
		raw, err = readPrivateFile(filepath.Join(sub.Dir, claudeCredentialsFile), r.ownUID())
	}
	if err != nil {
		return core.Login{}, wrapStoreError(sub, err)
	}

	var doc claudePayload
	if err := json.Unmarshal(raw, &doc); err != nil || doc.ClaudeAiOauth == nil {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "malformed credentials"}
	}
	tok := strings.TrimSpace(doc.ClaudeAiOauth.AccessToken)
	exp := doc.ClaudeAiOauth.ExpiresAt
	if tok == "" || exp <= 0 {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "missing token or expiry"}
	}
	expires := time.UnixMilli(exp)
	if exp < 1_000_000_000_000 {
		expires = time.Unix(exp, 0)
	}
	return core.Login{
		Authorization: "Bearer " + tok,
		Account:       sub.Account,
		Expires:       expires,
	}, nil
}

// claudeKeychainService returns the macOS keychain service name Claude Code uses
// for a configuration directory.
func claudeKeychainService(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return claudeKeychainBase
	}
	resolved := filepath.Clean(dir)
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	} else if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	sum := sha256.Sum256([]byte(resolved))
	return claudeKeychainBase + "-" + hex.EncodeToString(sum[:])[:8]
}

func readMacKeychain(ctx context.Context, service string) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultKeychainTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-w")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == keychainExitNotFound {
			return nil, ErrNotFound
		}
		return nil, ErrUnavailable
	}
	return out, nil
}

func wrapStoreError(sub Subscription, err error) error {
	var le *Error
	if errors.As(err, &le) {
		return err
	}
	kind := err
	switch {
	case errors.Is(err, ErrNotFound):
		kind = ErrNotFound
	case errors.Is(err, ErrUntrustedFile):
		kind = ErrUntrustedFile
	case errors.Is(err, ErrInvalid):
		kind = ErrInvalid
	case errors.Is(err, ErrUnsupportedStore):
		kind = ErrUnsupportedStore
	default:
		kind = ErrUnavailable
	}
	return &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: kind}
}
