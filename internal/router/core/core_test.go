package core_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/router/core"
	"github.com/looptender/looptender/internal/router/front"
)

var t0 = time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)

// logins is a stand-in for the login reader: made-up values only.
type logins struct {
	fail  map[string]bool
	asked []string
	now   *time.Time
}

func (l *logins) Login(_ context.Context, sub string) (core.Login, error) {
	l.asked = append(l.asked, sub)
	if l.fail[sub] {
		return core.Login{}, errors.New("unavailable")
	}
	return core.Login{Authorization: "Bearer made-up-" + sub, Account: "account-of-" + sub, Expires: l.now.Add(30 * time.Second)}, nil
}

type expired struct{ asked int }

func (e *expired) Login(context.Context, string) (core.Login, error) {
	e.asked++
	return core.Login{Authorization: "Bearer made-up", Account: "made-up", Expires: t0.Add(-time.Second)}, nil
}

func newRouter(l *logins) (*core.Router, *time.Time) {
	now := t0
	l.now = &now
	return core.New(core.Config{
		Order:  []string{"main", "spare", "third"},
		Home:   map[string]string{"client-main": "main", "client-spare": "spare"},
		Logins: l,
		Now:    func() time.Time { return now },
	}), &now
}

func decide(t *testing.T, r *core.Router, a front.Ask) front.Decision {
	t.Helper()
	d, err := r.Decide(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func limitHeaders(now time.Time, weekly string) map[string]string {
	return map[string]string{
		"anthropic-ratelimit-unified-7d-utilization": weekly,
		"anthropic-ratelimit-unified-7d-reset":       strconv.FormatInt(now.Add(72*time.Hour).Unix(), 10),
	}
}

func TestDecide(t *testing.T) {
	l := &logins{fail: map[string]bool{}}
	r, _ := newRouter(l)
	ask := front.Ask{Client: "client-main", Conversation: "c1", Model: "model-large"}

	// The home subscription has room: the session's own login, and the
	// front may reuse that for a while.
	if d := decide(t, r, ask); !d.Own || !d.Expires.Equal(t0.Add(time.Minute)) || len(l.asked) != 0 {
		t.Fatalf("%+v, logins asked %v", d, l.asked)
	}
	// A client whose home is not known is never routed.
	if d := decide(t, r, front.Ask{Client: "client-unknown", Conversation: "c9"}); !d.Own || !d.Expires.IsZero() {
		t.Fatalf("unknown client: %+v", d)
	}
	// The own login was just refused with a limit: the next subscription in
	// order serves, with its login and account, for no longer than the
	// login lasts.
	retry := ask
	retry.Retry, retry.ExcludeOwn = true, true
	d := decide(t, r, retry)
	if d.Own || d.Subscription != "spare" || d.Authorization != "Bearer made-up-spare" || d.Account != "account-of-spare" || !d.Expires.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("after the own login's limit: %+v", d)
	}
	// A subscription whose login cannot be had is left out and the next one
	// asked.
	l.fail["spare"] = true
	if d := decide(t, r, retry); d.Subscription != "third" {
		t.Fatalf("with the spare's login unavailable: %+v", d)
	}
	l.fail["third"] = true
	if d := decide(t, r, retry); !d.Own {
		t.Fatalf("with no login to be had: %+v", d)
	}
	if c := r.Counts(); c.LoginUnavailable != 3 || c.Routed != 2 {
		t.Fatalf("counts %+v", c)
	}
	// A login that has already run out is no login.
	stale := &expired{}
	r2 := core.New(core.Config{Order: []string{"main", "spare"}, Home: map[string]string{"client-main": "main"}, Logins: stale, Now: func() time.Time { return t0 }})
	if d := decide(t, r2, retry); !d.Own || stale.asked != 1 {
		t.Fatalf("with an expired login: %+v, asked %d", d, stale.asked)
	}
	// With no Logins at all nothing is ever routed.
	r3 := core.New(core.Config{Order: []string{"main", "spare"}, Home: map[string]string{"client-main": "main"}})
	if d := decide(t, r3, retry); !d.Own {
		t.Fatalf("with no source of logins: %+v", d)
	}
}

// What the front reports changes where conversations go: a limit holds the
// subscription, a reading near full sends new conversations elsewhere, a
// refusal takes the subscription out, and a server error changes nothing.
func TestReport(t *testing.T) {
	l := &logins{fail: map[string]bool{}}
	r, now := newRouter(l)
	ask := front.Ask{Client: "client-main", Conversation: "c1", Model: "model-large"}

	// A 500 on the home subscription is not a reason to go elsewhere.
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Status: 500})
	if d := decide(t, r, ask); !d.Own {
		t.Fatalf("after a server error: %+v", d)
	}
	// Served and pinned at home.
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Status: 200, Limits: limitHeaders(*now, "0.40")})
	// A limit on the home subscription: its conversations go to the spare.
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Status: 429, RetryAfter: "600"})
	if d := decide(t, r, ask); d.Subscription != "spare" {
		t.Fatalf("after a limit at home: %+v", d)
	}
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Subscription: "spare", Status: 200, Limits: limitHeaders(*now, "0.10")})
	// After the hold the conversation is still on the spare while it is in
	// use (mid-turn it stays where it is)...
	*now = now.Add(10*time.Minute + 30*time.Second)
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Subscription: "spare", Status: 200})
	*now = now.Add(30 * time.Second)
	if d := decide(t, r, ask); d.Subscription != "spare" {
		t.Fatalf("mid-turn after the home's hold ended: %+v", d)
	}
	// ...and returns home once it has been quiet for longer than a cache
	// lasts.
	quiet := ask
	quiet.Conversation = "c-quiet"
	r.Report(front.Outcome{Client: "client-main", Conversation: "c-quiet", Subscription: "spare", Status: 200})
	*now = now.Add(10 * time.Minute)
	if d := decide(t, r, quiet); !d.Own {
		t.Fatalf("a quiet conversation did not return home: %+v", d)
	}
	// The provider refuses the spare: nothing is routed to it again.
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Subscription: "spare", Status: 403})
	if d := decide(t, r, ask); d.Subscription == "spare" {
		t.Fatalf("after the spare was refused: %+v", d)
	}
	// A rejected login on the third takes it out too.
	r.Report(front.Outcome{Client: "client-main", Conversation: "c2", Subscription: "third", Status: 401})
	retry := front.Ask{Client: "client-main", Conversation: "c2", Retry: true, ExcludeOwn: true}
	if d := decide(t, r, retry); !d.Own {
		t.Fatalf("with every other subscription out: %+v", d)
	}
	// A reading near full at home sends a new conversation of the spare's
	// own client... nowhere it cannot go: the spare is refused, so its own
	// login is all there is.
	if d := decide(t, r, front.Ask{Client: "client-spare", Conversation: "c3"}); d.Subscription != "main" && !d.Own {
		t.Fatalf("%+v", d)
	}
	// The front's events are counted.
	for _, e := range []string{front.StreamCut, front.UpstreamTimeout, front.Unreachable, front.RetryExhausted} {
		r.Report(front.Outcome{Client: "client-main", Event: e})
	}
	if c := r.Counts(); c.StreamCuts != 1 || c.Timeouts != 1 || c.Unreachable != 1 || c.RetriesExhausted != 1 {
		t.Fatalf("counts %+v", c)
	}
	// An outcome from a client nobody knows changes nothing and does not
	// fail.
	r.Report(front.Outcome{Client: "client-unknown", Status: 429})
}

// A reading from an answer's headers moves new conversations before any
// limit is hit.
func TestReadingNearFullSendsNewConversationsElsewhere(t *testing.T) {
	l := &logins{fail: map[string]bool{}}
	r, now := newRouter(l)
	r.Report(front.Outcome{Client: "client-main", Conversation: "c1", Status: 200, Limits: limitHeaders(*now, "0.95")})
	r.Report(front.Outcome{Client: "client-spare", Conversation: "s1", Status: 200, Limits: limitHeaders(*now, "0.10")})
	if d := decide(t, r, front.Ask{Client: "client-main", Conversation: "c2"}); d.Subscription != "spare" {
		t.Fatalf("a new conversation with the home at 95%%: %+v", d)
	}
	// The conversation already there stays, mid-turn.
	if d := decide(t, r, front.Ask{Client: "client-main", Conversation: "c1"}); !d.Own {
		t.Fatalf("the conversation already at home: %+v", d)
	}
	// Headers in a form that is not known are no reading at all.
	r2, now2 := newRouter(l)
	r2.Report(front.Outcome{Client: "client-main", Conversation: "c1", Status: 200, Limits: map[string]string{"anthropic-ratelimit-unified-7d-utilization": "95"}})
	if d := decide(t, r2, front.Ask{Client: "client-main", Conversation: "c2"}); !d.Own {
		t.Fatalf("an unreadable figure was taken as a reading at %v: %+v", *now2, d)
	}
}
