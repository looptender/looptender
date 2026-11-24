// Package core is the account router's deciding process, without anything
// that touches the outside: it answers the front's question with the
// routing decision (internal/router/route), takes in what the front reports,
// and asks a Logins for a subscription's login when another subscription is
// to serve. It reads no credential itself: Logins is an interface, and
// nothing in this package implements it.
package core

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/looptender/looptender/internal/router/front"
	"github.com/looptender/looptender/internal/router/limits"
	"github.com/looptender/looptender/internal/router/route"
)

// Login is what a routed request needs of the serving subscription. It is
// held only as long as a decision is made and is never logged, stored or put
// in an error.
type Login struct {
	// Authorization is the whole header value.
	Authorization string
	// Account is the subscription's account id.
	Account string
	// Expires is when the login stops being usable.
	Expires time.Time
}

// Logins gives a subscription's login. An error means the subscription
// cannot serve now; it must name no value.
type Logins interface {
	Login(ctx context.Context, subscription string) (Login, error)
}

// Config is what a Router needs.
type Config struct {
	// Order is the person's order of subscriptions.
	Order []string
	// Home says which subscription each client's own login belongs to.
	Home map[string]string
	// Settings are the thresholds; the zero value means route.Defaults.
	Settings route.Settings
	Logins   Logins
	Now      func() time.Time
	// Reuse is how long the front may reuse a decision without asking.
	Reuse time.Duration
}

// Counts are tallies for status.
type Counts struct {
	Decisions, Routed, NoRoom int64
	// LoginUnavailable counts decisions that skipped a subscription because
	// its login could not be had.
	LoginUnavailable int64
	// StreamCuts, Timeouts, Unreachable and RetriesExhausted count what the
	// front reported.
	StreamCuts, Timeouts, Unreachable, RetriesExhausted int64
}

// Router decides and remembers. It is safe for use from many goroutines.
type Router struct {
	cfg Config

	mu      sync.Mutex
	st      route.State
	pending map[string]route.Reason // why a conversation was last sent where it was
	counts  Counts
}

// New returns a router for cfg.
func New(cfg Config) *Router {
	if cfg.Settings == (route.Settings{}) {
		cfg.Settings = route.Defaults()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Reuse <= 0 {
		cfg.Reuse = time.Minute
	}
	r := &Router{cfg: cfg, pending: map[string]route.Reason{}, st: route.State{Pins: map[string]route.Pin{}}}
	for _, id := range cfg.Order {
		r.st.Order = append(r.st.Order, route.Subscription{ID: id})
	}
	return r
}

// Counts returns the router's tallies.
func (r *Router) Counts() Counts {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts
}

func conversation(client, id string) string {
	if id == "" {
		return ""
	}
	return client + "\x00" + id
}

var ownDecision = front.Decision{Own: true}

// Decide answers the front. Every way of not being able to route is the
// answer "your own login": no room anywhere, a client whose home is not
// known, a subscription whose login cannot be had.
func (r *Router) Decide(ctx context.Context, ask front.Ask) (front.Decision, error) {
	now := r.cfg.Now()
	r.mu.Lock()
	r.counts.Decisions++
	home, known := r.cfg.Home[ask.Client]
	r.mu.Unlock()
	if !known {
		return ownDecision, nil
	}
	key := conversation(ask.Client, ask.Conversation)
	exclude := append([]string(nil), ask.Exclude...)
	if ask.ExcludeOwn {
		exclude = append(exclude, home)
	}
	// At most one try per subscription: one whose login cannot be had is
	// left out and the choice made again.
	for range r.cfg.Order {
		r.mu.Lock()
		d := route.Choose(r.st, r.cfg.Settings, route.Request{Conversation: key, Model: ask.Model, Now: now, Exclude: exclude})
		if d.Reason == route.NoRoom {
			r.counts.NoRoom++
			r.mu.Unlock()
			return ownDecision, nil
		}
		if key != "" {
			r.pending[key] = d.Reason
		}
		r.mu.Unlock()
		if d.Subscription == home {
			return front.Decision{Own: true, Expires: now.Add(r.cfg.Reuse)}, nil
		}
		if r.cfg.Logins == nil {
			return ownDecision, nil
		}
		login, err := r.cfg.Logins.Login(ctx, d.Subscription)
		if err != nil || login.Authorization == "" || !login.Expires.After(now) {
			r.mu.Lock()
			r.counts.LoginUnavailable++
			r.mu.Unlock()
			exclude = append(exclude, d.Subscription)
			continue
		}
		expires := now.Add(r.cfg.Reuse)
		if login.Expires.Before(expires) {
			expires = login.Expires
		}
		r.mu.Lock()
		r.counts.Routed++
		r.mu.Unlock()
		return front.Decision{Subscription: d.Subscription, Authorization: login.Authorization, Account: login.Account, Expires: expires}, nil
	}
	return ownDecision, nil
}

// Report takes in how a request went: what the answer's headers say of the
// serving subscription's windows, and what its status means for that
// subscription. Only a limit holds a subscription; a refusal or a rejected
// login takes it out of the order until a person clears it; anything else
// changes nothing.
func (r *Router) Report(o front.Outcome) {
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	switch o.Event {
	case front.StreamCut:
		r.counts.StreamCuts++
	case front.UpstreamTimeout:
		r.counts.Timeouts++
	case front.Unreachable:
		r.counts.Unreachable++
	case front.RetryExhausted:
		r.counts.RetriesExhausted++
	}
	sub := o.Subscription
	if sub == "" {
		home, known := r.cfg.Home[o.Client]
		if !known {
			return
		}
		sub = home
	}
	set := r.cfg.Settings
	if reading := limits.Claude(o.Limits, now); len(reading.Windows) > 0 {
		route.Observe(&r.st, set, sub, route.Observation{Kind: route.Reading, At: now, Windows: reading.Windows})
	}
	key := conversation(o.Client, o.Conversation)
	switch {
	case o.Status == 429:
		route.Observe(&r.st, set, sub, route.Observation{Kind: route.Limited, At: now, RetryAfter: retryAfter(o.RetryAfter)})
	case o.Status == 401:
		route.Observe(&r.st, set, sub, route.Observation{Kind: route.Unauthorized, At: now})
	case o.Status == 403:
		route.Observe(&r.st, set, sub, route.Observation{Kind: route.Restricted, At: now})
	case o.Status >= 200 && o.Status < 300 && o.Event == "":
		why, ok := r.pending[key]
		if !ok {
			why = route.Pinned
		}
		route.Served(&r.st, set, key, sub, why, now)
	}
	delete(r.pending, key)
}

// retryAfter reads a wait given in whole seconds, zero when it is not one.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 7 || strings.Trim(v, "0123456789") != "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return time.Duration(n) * time.Second
}
