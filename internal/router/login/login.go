// Package login reads existing local credentials for configured subscriptions,
// read-only and in memory only.
//
// It never logs in, never refreshes tokens, never copies or writes credentials
// to any store, and never includes token or account values in errors, logs,
// or serialized output.
package login

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/looptender/looptender/internal/router/core"
)

// Provider names matching router configuration.
const (
	ProviderClaude = "claude-code"
	ProviderCodex  = "codex"
)

var (
	// ErrUnknownSubscription means the subscription name is not configured.
	ErrUnknownSubscription = errors.New("unknown subscription")
	// ErrUnsupportedProvider means the subscription's provider has no login reader.
	ErrUnsupportedProvider = errors.New("unsupported provider")
	// ErrUnsupportedStore means the subscription uses a credential store this reader does not read.
	ErrUnsupportedStore = errors.New("unsupported credential store")
	// ErrNotFound means no login is present in the subscription's store.
	ErrNotFound = errors.New("login not found")
	// ErrUnavailable means the store could not be read right now (for example, locked or timed out).
	ErrUnavailable = errors.New("login store unavailable")
	// ErrUntrustedFile means the credential file or its directory failed permission, type, or owner checks.
	ErrUntrustedFile = errors.New("untrusted credential file or directory")
	// ErrInvalid means the credential payload is malformed or missing required fields.
	ErrInvalid = errors.New("invalid credential payload")
	// ErrExpired means the stored access token is already expired.
	ErrExpired = errors.New("login expired")
)

// Error is a structured error from reading a subscription's login.
// It never contains token values, refresh tokens, or account identifiers.
type Error struct {
	Subscription string
	Provider     string
	Kind         error
	Detail       string
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("login %s (%s): %v: %s", e.Subscription, e.Provider, e.Kind, e.Detail)
	}
	return fmt.Sprintf("login %s (%s): %v", e.Subscription, e.Provider, e.Kind)
}

func (e *Error) Unwrap() error { return e.Kind }

// Subscription describes one configured subscription whose existing login may be read.
type Subscription struct {
	ID       string
	Provider string
	// Dir is the subscription's configuration directory (CLAUDE_CONFIG_DIR or CODEX_HOME).
	Dir string
	// Account is an optional configured account identifier (used when the store does not carry one).
	Account string
}

// Config configures a Reader.
type Config struct {
	Subscriptions []Subscription
	Now           func() time.Time
	// OS overrides runtime.GOOS for tests; empty defaults to runtime.GOOS.
	OS string
	// Keychain reads a macOS keychain generic password by service name.
	// When nil on darwin, the default security(1) reader is used.
	Keychain func(ctx context.Context, service string) ([]byte, error)
	// OwnUID overrides os.Getuid() for file ownership checks in tests.
	OwnUID func() int
}

// Reader implements core.Logins by reading existing credentials from each
// subscription's own store and caching them in memory until expiry.
type Reader struct {
	cfg    Config
	subs   map[string]Subscription
	osName string
	now    func() time.Time
	ownUID func() int
	mu     sync.Mutex
	cached map[string]core.Login
}

var _ core.Logins = (*Reader)(nil)

// New returns a Reader for cfg.
func New(cfg Config) *Reader {
	subs := make(map[string]Subscription, len(cfg.Subscriptions))
	for _, s := range cfg.Subscriptions {
		if s.ID != "" {
			subs[s.ID] = s
		}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	osName := cfg.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	ownUID := cfg.OwnUID
	if ownUID == nil {
		ownUID = defaultOwnUID
	}
	return &Reader{
		cfg:    cfg,
		subs:   subs,
		osName: osName,
		now:    now,
		ownUID: ownUID,
		cached: make(map[string]core.Login),
	}
}

// Login returns the subscription's current access token and expiry from memory
// if still valid, or reads it read-only from the subscription's own store.
func (r *Reader) Login(ctx context.Context, subscription string) (core.Login, error) {
	sub, ok := r.subs[subscription]
	if !ok {
		return core.Login{}, &Error{Subscription: subscription, Kind: ErrUnknownSubscription}
	}
	now := r.now()

	r.mu.Lock()
	if c, ok := r.cached[subscription]; ok && c.Authorization != "" && c.Expires.After(now) {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	var (
		got core.Login
		err error
	)
	switch sub.Provider {
	case ProviderClaude:
		got, err = r.readClaude(ctx, sub)
	case ProviderCodex:
		got, err = r.readCodex(sub)
	default:
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrUnsupportedProvider}
	}
	if err != nil {
		return core.Login{}, err
	}
	if !got.Expires.After(now) {
		r.Invalidate(subscription)
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrExpired}
	}

	r.mu.Lock()
	r.cached[subscription] = got
	r.mu.Unlock()
	return got, nil
}

// Invalidate drops any in-memory cached login for subscription.
func (r *Reader) Invalidate(subscription string) {
	r.mu.Lock()
	delete(r.cached, subscription)
	r.mu.Unlock()
}
