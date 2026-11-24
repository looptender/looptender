package front_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/router/front"
	"github.com/looptender/looptender/internal/router/standin"
)

// Made-up logins. The session's own is "own-login"; the router can name two
// more.
const (
	ownLogin   = "sk-ant-oat01-own-login"
	spareLogin = "sk-ant-oat01-spare-login"
	thirdLogin = "sk-ant-oat01-third-login"
	secret     = "client-secret-for-tests"
	convA      = "11111111-2222-4333-8444-555555555555"
	// Made-up account ids: the session's own and the spare's.
	ownAccount   = "99999999-2222-4333-8444-555555555555"
	spareAccount = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// router is a stand-in for the router: it answers as decide says and keeps
// what it was asked and told.
type router struct {
	mu       sync.Mutex
	decide   func(context.Context, front.Ask) (front.Decision, error)
	asks     []front.Ask
	outcomes chan front.Outcome
}

func (r *router) Decide(ctx context.Context, a front.Ask) (front.Decision, error) {
	r.mu.Lock()
	r.asks = append(r.asks, a)
	decide := r.decide
	r.mu.Unlock()
	return decide(ctx, a)
}

func (r *router) Report(o front.Outcome) { r.outcomes <- o }

func (r *router) set(decide func(context.Context, front.Ask) (front.Decision, error)) {
	r.mu.Lock()
	r.decide = decide
	r.mu.Unlock()
}

func (r *router) asked() []front.Ask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]front.Ask(nil), r.asks...)
}

func (r *router) outcome(t *testing.T) front.Outcome {
	t.Helper()
	select {
	case o := <-r.outcomes:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("no outcome was reported")
		return front.Outcome{}
	}
}

func always(d front.Decision) func(context.Context, front.Ask) (front.Decision, error) {
	return func(context.Context, front.Ask) (front.Decision, error) { return d, nil }
}

func to(subscription, login string) front.Decision {
	return front.Decision{Subscription: subscription, Authorization: "Bearer " + login, Account: spareAccount}
}

func noRouter(context.Context, front.Ask) (front.Decision, error) {
	return front.Decision{}, front.ErrNoRouter
}

// rig is a front between a stand-in provider and a stand-in router.
type rig struct {
	t      *testing.T
	up     *standin.Upstream
	router *router
	front  *front.Front
	addr   string
	upHost string

	mu      sync.Mutex
	bodies  []string
	headers []http.Header
}

func (g *rig) sent() ([]string, []http.Header) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.bodies...), append([]http.Header(nil), g.headers...)
}

func start(t *testing.T, change func(*front.Config)) *rig {
	t.Helper()
	up := &standin.Upstream{}
	g := &rig{t: t, up: up}
	// The provider as the tests see it: the stand-in, with the bodies and
	// headers it was sent kept for the tests that compare them. Everything
	// here is made up.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.bodies, g.headers = append(g.bodies, string(b)), append(g.headers, r.Header.Clone())
		g.mu.Unlock()
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		up.ServeHTTP(w, r)
	}))
	t.Cleanup(provider.Close)
	u, _ := url.Parse(provider.URL)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rt := &router{outcomes: make(chan front.Outcome, 32), decide: always(front.Decision{Own: true})}
	cfg := front.Config{
		Upstream: u,
		Port:     ln.Addr().(*net.TCPAddr).Port,
		Client: func(s string) (string, bool) {
			return "client-one", s == secret
		},
	}
	if change != nil {
		change(&cfg)
	}
	f, err := front.New(cfg, rt)
	if err != nil {
		t.Fatal(err)
	}
	go f.Serve(ln)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		f.Stop(ctx)
	})
	g.router, g.front, g.addr, g.upHost = rt, f, ln.Addr().String(), u.Host
	return g
}

type call struct {
	path, secret, login, conversation, host string
	// body replaces the usual request body.
	body   string
	header map[string]string
	method string
}

// do sends one request as a session would and returns the answer with its
// whole body; err is the error reading the body, if any.
func (g *rig) do(c call) (resp *http.Response, body string, err error) {
	g.t.Helper()
	if c.path == "" {
		c.path = "/v1/messages"
	}
	if c.method == "" {
		c.method = http.MethodPost
	}
	if c.login == "" {
		c.login = ownLogin
	}
	if c.conversation == "" {
		c.conversation = convA
	}
	payload := payloadFor(c.conversation)
	if c.body != "" {
		payload = c.body
	}
	req, _ := http.NewRequest(c.method, "http://"+g.addr+c.path, strings.NewReader(payload))
	if c.host != "" {
		req.Host = c.host
	}
	req.Header.Set("Authorization", "Bearer "+c.login)
	req.Header.Set("Content-Type", "application/json")
	if c.secret != "-" {
		if c.secret == "" {
			c.secret = secret
		}
		req.Header.Set(front.SecretHeader, c.secret)
	}
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 20 * time.Second}
	resp, err = client.Do(req)
	if err != nil {
		g.t.Fatalf("the front did not answer: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

// payloadFor is a request body shaped like Claude Code's: user_id is a JSON
// object inside a string, naming a device, the account and the session.
func payloadFor(conversation string) string {
	return fmt.Sprintf(`{"model":"model-large","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"a {nested} \"prompt\" with caf\u00e9"}]}],"metadata":{"user_id":"{\"device_id\":\"%s\",\"account_uuid\":\"%s\",\"session_id\":\"%s\"}"}}`, strings.Repeat("ab", 32), ownAccount, conversation)
}

func (g *rig) ok(c call) string {
	g.t.Helper()
	resp, body, err := g.do(c)
	if err != nil || resp.StatusCode != http.StatusOK {
		g.t.Fatalf("status %d, body error %v, body %q", resp.StatusCode, err, body)
	}
	return body
}

func events(body string) int { return strings.Count(body, "event: message") }

// R1: no router. The request goes on its own login, and the front does not
// ask again for every request.
func TestRouterAbsentGoesOnOwnLogin(t *testing.T) {
	g := start(t, nil)
	g.router.set(noRouter)
	for i := 0; i < 3; i++ {
		if n := events(g.ok(call{})); n != 3 {
			t.Fatalf("got %d events", n)
		}
	}
	if n := g.up.Served(ownLogin); n != 3 {
		t.Fatalf("own login served %d of 3", n)
	}
	if asks := g.router.asked(); len(asks) != 1 {
		t.Fatalf("asked %d times, want once and then a pause", len(asks))
	}
	if c := g.front.Counts(); c.Skipped != 3 || c.Missed != 0 {
		t.Fatalf("counts %+v", c)
	}
}

// R17: a router that answers late. The request goes on its own login after
// the bound, the miss is counted, and the front keeps asking.
func TestSlowDecisionGoesOnOwnLogin(t *testing.T) {
	g := start(t, func(c *front.Config) { c.FirstWait, c.MaxWait = 30*time.Millisecond, 60*time.Millisecond })
	g.router.set(func(ctx context.Context, _ front.Ask) (front.Decision, error) {
		<-ctx.Done()
		return to("spare", spareLogin), nil
	})
	g.ok(call{})
	g.ok(call{})
	if g.up.Served(ownLogin) != 2 || g.up.Served(spareLogin) != 0 {
		t.Fatalf("own %d, spare %d", g.up.Served(ownLogin), g.up.Served(spareLogin))
	}
	if len(g.router.asked()) != 2 || g.front.Counts().Missed != 2 {
		t.Fatalf("asked %d, counts %+v: a slow router must still be asked", len(g.router.asked()), g.front.Counts())
	}
}

// A routed request carries the named subscription's login and nothing of
// the front's own; a decision that may be reused is not asked for again.
func TestRoutedRequestCarriesTheNamedLogin(t *testing.T) {
	g := start(t, nil)
	d := to("spare", spareLogin)
	d.Expires = time.Now().Add(time.Hour)
	g.router.set(always(d))
	g.ok(call{header: map[string]string{"X-Api-Key": "sk-ant-api03-left-over", "Proxy-Authorization": "Basic abc", "Connection": "X-Private", "X-Private": "1"}})
	g.ok(call{})
	if g.up.Served(spareLogin) != 2 || g.up.Served(ownLogin) != 0 {
		t.Fatalf("spare %d, own %d", g.up.Served(spareLogin), g.up.Served(ownLogin))
	}
	asks := g.router.asked()
	if len(asks) != 1 {
		t.Fatalf("asked %d times for one conversation with a reusable decision", len(asks))
	}
	if a := asks[0]; a.Client != "client-one" || a.Conversation != convA || a.Model != "model-large" || a.Retry {
		t.Fatalf("asked %+v", a)
	}
	rec := g.up.Records()[0]
	for _, h := range rec.Headers {
		switch h {
		case front.SecretHeader, "X-Api-Key", "Proxy-Authorization", "X-Private", "Connection":
			t.Fatalf("%s reached the provider", h)
		}
	}
	if rec.Host != g.upHost {
		t.Fatalf("provider saw Host %q, want its own %q", rec.Host, g.upHost)
	}
	if c := rec.Credential; c.Carrier != "authorization" || c.Scheme != "bearer" || c.Kind != "oauth_access" || c.PrefixLen != len("sk-ant-oat01-") || c.Len != len(spareLogin) {
		t.Fatalf("credential shape %+v", c)
	}
	o := g.router.outcome(t)
	if o.Subscription != "spare" || o.Status != 200 || o.Conversation != convA || o.Model != "model-large" || o.Event != "" || o.RequestBytes == 0 {
		t.Fatalf("outcome %+v", o)
	}
	// Replacing and forgetting the held decision.
	g.front.Replace("client-one", convA, front.Decision{Own: true, Expires: time.Now().Add(time.Hour)})
	g.ok(call{})
	if g.up.Served(ownLogin) != 1 {
		t.Fatal("a replaced decision was not used")
	}
	g.front.Forget("client-one", convA)
	g.ok(call{})
	if len(g.router.asked()) != 2 {
		t.Fatal("a forgotten decision was not asked for again")
	}
}

// R2: the router goes away while an answer is streaming. Nothing happens to
// the stream.
func TestRouterGoneMidStream(t *testing.T) {
	g := start(t, nil)
	g.router.set(always(to("spare", spareLogin)))
	g.up.Script(spareLogin, standin.Step{Events: 6, Gap: 30 * time.Millisecond})
	done := make(chan string, 1)
	go func() { done <- g.ok(call{}) }()
	time.Sleep(60 * time.Millisecond)
	g.router.set(noRouter)
	if n := events(<-done); n != 6 {
		t.Fatalf("stream had %d of 6 events", n)
	}
}

// A limit answer before the first byte: asked once more, with a longer
// wait, and served by the next subscription.
func TestLimitBeforeFirstByteIsRetriedOnce(t *testing.T) {
	g := start(t, func(c *front.Config) { c.FirstWait, c.MaxWait = 30*time.Millisecond, 30*time.Millisecond })
	g.up.Script(spareLogin, standin.Step{Status: 429, Header: map[string]string{"Retry-After": "120", "Anthropic-Ratelimit-Unified-Status": "rejected"}, Body: `{"type":"error"}`})
	g.router.set(func(ctx context.Context, a front.Ask) (front.Decision, error) {
		if !a.Retry {
			return to("spare", spareLogin), nil
		}
		// Slower than the first bound, within the retry's.
		select {
		case <-time.After(80 * time.Millisecond):
		case <-ctx.Done():
			return front.Decision{}, ctx.Err()
		}
		return to("third", thirdLogin), nil
	})
	if n := events(g.ok(call{})); n != 3 {
		t.Fatalf("got %d events", n)
	}
	asks := g.router.asked()
	if len(asks) != 2 || !asks[1].Retry || len(asks[1].Exclude) != 1 || asks[1].Exclude[0] != "spare" || asks[1].ExcludeOwn {
		t.Fatalf("asks %+v", asks)
	}
	first, second := g.router.outcome(t), g.router.outcome(t)
	if first.Status != 429 {
		first, second = second, first
	}
	if first.Subscription != "spare" || first.Status != 429 || first.RetryAfter != "120" || first.Limits["anthropic-ratelimit-unified-status"] != "rejected" {
		t.Fatalf("first outcome %+v", first)
	}
	if second.Subscription != "third" || second.Status != 200 || second.Event != "" {
		t.Fatalf("second outcome %+v", second)
	}
}

// The session's own login at its limit is the moment routing matters most:
// the router is asked, and told that the own login refused.
func TestLimitOnOwnLoginAsksTheRouter(t *testing.T) {
	g := start(t, nil)
	g.up.Script(ownLogin, standin.Step{Status: 429, Body: `{"own":"limit"}`})
	g.router.set(func(_ context.Context, a front.Ask) (front.Decision, error) {
		if a.Retry && a.ExcludeOwn {
			return to("spare", spareLogin), nil
		}
		return front.Decision{Own: true}, nil
	})
	g.ok(call{})
	if g.up.Served(ownLogin) != 1 || g.up.Served(spareLogin) != 1 {
		t.Fatalf("own %d, spare %d", g.up.Served(ownLogin), g.up.Served(spareLogin))
	}
}

// R10: nowhere has room. The session gets the provider's own limit answer,
// never one of the front's, and nothing is sent twice to the same login.
func TestNoRoomPassesTheProvidersAnswer(t *testing.T) {
	g := start(t, nil)
	g.up.Script(ownLogin, standin.Step{Status: 429, Header: map[string]string{"Retry-After": "7"}, Body: `{"own":"limit"}`})
	g.up.Script(spareLogin, standin.Step{Status: 429, Body: `{"spare":"limit"}`})

	resp, body, _ := g.do(call{})
	if resp.StatusCode != 429 || body != `{"own":"limit"}` || resp.Header.Get("Retry-After") != "7" || g.up.Served(ownLogin) != 1 {
		t.Fatalf("own login only: %d %q, sent %d times", resp.StatusCode, body, g.up.Served(ownLogin))
	}

	g.router.set(func(_ context.Context, a front.Ask) (front.Decision, error) {
		if a.Retry {
			return front.Decision{Own: true}, nil
		}
		return to("spare", spareLogin), nil
	})
	resp, body, _ = g.do(call{conversation: "22222222-2222-4333-8444-555555555555"})
	if resp.StatusCode != 429 || body != `{"own":"limit"}` {
		t.Fatalf("after the retry: %d %q", resp.StatusCode, body)
	}
	var exhausted bool
	for i := 0; i < 3; i++ {
		if o := g.router.outcome(t); o.Event == front.RetryExhausted {
			exhausted = true
		}
	}
	if !exhausted {
		t.Fatal("an exhausted retry was not reported")
	}
}

// R8: the provider rejects a routed login. The request is sent again
// unchanged on the session's own login, without asking. A rejection of the
// session's own login is passed through.
func TestRejectedLogin(t *testing.T) {
	g := start(t, nil)
	g.up.Script(spareLogin, standin.Step{Status: 401, Body: `{"spare":"rejected"}`})
	g.router.set(always(to("spare", spareLogin)))
	g.ok(call{})
	if len(g.router.asked()) != 1 || g.up.Served(ownLogin) != 1 {
		t.Fatalf("asked %d, own served %d", len(g.router.asked()), g.up.Served(ownLogin))
	}
	if o := g.router.outcome(t); o.Subscription != "spare" || o.Status != 401 {
		t.Fatalf("outcome %+v", o)
	}

	g.router.set(always(front.Decision{Own: true}))
	g.up.Script(ownLogin, standin.Step{Status: 401, Body: `{"own":"rejected"}`})
	resp, body, _ := g.do(call{})
	if resp.StatusCode != 401 || body != `{"own":"rejected"}` || g.up.Served(ownLogin) != 2 {
		t.Fatalf("%d %q, own served %d", resp.StatusCode, body, g.up.Served(ownLogin))
	}
}

// R9, R18: a refusal and a server error are passed through and never cause
// a second question or a second request.
func TestRefusalAndServerErrorAreNotLimits(t *testing.T) {
	for _, status := range []int{403, 500, 529} {
		g := start(t, nil)
		g.up.Script(spareLogin, standin.Step{Status: status, Body: `{"answer":"provider"}`})
		g.router.set(always(to("spare", spareLogin)))
		resp, body, _ := g.do(call{})
		if resp.StatusCode != status || body != `{"answer":"provider"}` {
			t.Fatalf("%d: got %d %q", status, resp.StatusCode, body)
		}
		if len(g.router.asked()) != 1 || len(g.up.Records()) != 1 {
			t.Fatalf("%d: asked %d, sent %d", status, len(g.router.asked()), len(g.up.Records()))
		}
		if o := g.router.outcome(t); o.Status != status || o.Subscription != "spare" {
			t.Fatalf("%d: outcome %+v", status, o)
		}
	}
}

// R23: the provider drops a stream midway. The session does not get an
// answer that looks complete, and the cut is reported as one.
func TestStreamCutByProviderIsNotACleanEnd(t *testing.T) {
	g := start(t, nil)
	g.up.Script(ownLogin, standin.Step{Events: 5, CutAfter: 2})
	resp, body, err := g.do(call{})
	if resp.StatusCode != 200 || err == nil {
		t.Fatalf("status %d, body error %v: a cut stream ended cleanly (%d events)", resp.StatusCode, err, events(body))
	}
	if o := g.router.outcome(t); o.Event != front.StreamCut || o.Status != 200 {
		t.Fatalf("outcome %+v", o)
	}
	if len(g.up.Records()) != 1 {
		t.Fatal("a request was sent again after its first byte")
	}
}

// R24: the provider hangs before answering, and goes silent inside a
// stream.
func TestProviderHangsAreBounded(t *testing.T) {
	g := start(t, func(c *front.Config) { c.HeaderWait, c.IdleWait = 80*time.Millisecond, 150*time.Millisecond })
	g.up.Script(ownLogin, standin.Step{Hang: true}, standin.Step{Events: 5, SilentAfter: 1})

	begin := time.Now()
	resp, body, _ := g.do(call{})
	if resp.StatusCode != http.StatusGatewayTimeout || !strings.Contains(body, "looptender router front") || time.Since(begin) > 5*time.Second {
		t.Fatalf("hang: %d %q after %v", resp.StatusCode, body, time.Since(begin))
	}
	if o := g.router.outcome(t); o.Event != front.UpstreamTimeout {
		t.Fatalf("outcome %+v", o)
	}

	resp, body, err := g.do(call{})
	if resp.StatusCode != 200 || err == nil || events(body) != 1 {
		t.Fatalf("silence: status %d, body error %v, %d events", resp.StatusCode, err, events(body))
	}
	if o := g.router.outcome(t); o.Event != front.UpstreamTimeout {
		t.Fatalf("outcome %+v", o)
	}
}

// R18: no connection to the provider. The front says so itself, with a
// status a client retries, and never with 401 or 429.
func TestProviderUnreachable(t *testing.T) {
	dead, _ := net.Listen("tcp4", "127.0.0.1:0")
	u, _ := url.Parse("http://" + dead.Addr().String())
	dead.Close()
	g := start(t, func(c *front.Config) { c.Upstream = u })
	resp, body, _ := g.do(call{})
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "looptender router front") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if o := g.router.outcome(t); o.Event != front.Unreachable {
		t.Fatalf("outcome %+v", o)
	}
}

// Stopping lets an open stream finish; a stop that cannot wait says how
// many it cut.
func TestStopDrainsOpenStreams(t *testing.T) {
	g := start(t, nil)
	g.up.Script(ownLogin, standin.Step{Events: 5, Gap: 40 * time.Millisecond})
	done := make(chan string, 1)
	go func() { done <- g.ok(call{}) }()
	time.Sleep(60 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	open, cut := g.front.Stop(ctx)
	if open != 1 || cut != 0 {
		t.Fatalf("open %d, cut %d", open, cut)
	}
	if n := events(<-done); n != 5 {
		t.Fatalf("drained stream had %d of 5 events", n)
	}

	g = start(t, nil)
	g.up.Script(ownLogin, standin.Step{Events: 3, Gap: 2 * time.Second})
	go g.do(call{})
	time.Sleep(100 * time.Millisecond)
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if open, cut := g.front.Stop(ctx); open != 1 || cut != 1 {
		t.Fatalf("impatient stop: open %d, cut %d", open, cut)
	}
}

// A body over the cap is forwarded whole and unrouted.
func TestBodyOverTheCapPassesThrough(t *testing.T) {
	g := start(t, func(c *front.Config) { c.MaxBody = 64 })
	g.router.set(always(to("spare", spareLogin)))
	g.ok(call{})
	rec := g.up.Records()
	if len(g.router.asked()) != 0 || g.up.Served(ownLogin) != 1 || rec[0].BodyBytes < 200 {
		t.Fatalf("asked %d, own served %d, body %d bytes", len(g.router.asked()), g.up.Served(ownLogin), rec[0].BodyBytes)
	}
}

// Who may be routed. An unknown or missing secret is not refused: the
// request goes through on its own login, so a stale secret never breaks a
// session. Other paths and methods pass through the same way.
func TestOnlyAKnownClientOnAMessagePathIsRouted(t *testing.T) {
	g := start(t, nil)
	g.router.set(always(to("spare", spareLogin)))
	for _, c := range []call{
		{secret: "an-old-secret"},
		{secret: "-"},
		{path: "/v1/models", method: http.MethodGet},
		{path: "/v1/something/new"},
		{path: "/api/hello"},
		// Read-only calls a client makes for itself share a prefix with the
		// sign-in paths and are not refused for it.
		{path: "/api/oauth/profile", method: http.MethodGet},
		{path: "/api/oauth/account/settings", method: http.MethodGet},
		{path: "/v1/models/a%2Fb", method: http.MethodGet},
	} {
		g.ok(c)
	}
	if got := g.up.Records()[7].Path; got != "/v1/models/a%2Fb" {
		t.Fatalf("an escaped slash reached the provider as %q", got)
	}
	if len(g.router.asked()) != 0 || g.up.Served(ownLogin) != 8 || g.up.Served(spareLogin) != 0 {
		t.Fatalf("asked %d, own %d, spare %d", len(g.router.asked()), g.up.Served(ownLogin), g.up.Served(spareLogin))
	}
	for _, rec := range g.up.Records() {
		for _, h := range rec.Headers {
			if h == front.SecretHeader {
				t.Fatal("the client secret reached the provider")
			}
		}
	}
	g.ok(call{path: "/v1/messages/count_tokens"})
	if g.up.Served(spareLogin) != 1 {
		t.Fatal("count_tokens was not routed")
	}
}

// What the front refuses by itself, always with 403 and never reaching the
// provider.
func TestGuards(t *testing.T) {
	g := start(t, nil)
	for name, c := range map[string]call{
		"another host":         {host: "example.com"},
		"another port":         {host: "127.0.0.1:1"},
		"a web page":           {header: map[string]string{"Origin": "https://example.com"}},
		"exchanging a token":   {path: "/v1/oauth/token"},
		"granting a token":     {path: "/api/oauth/authorize", method: http.MethodGet},
		"making a key":         {path: "/api/oauth/claude_cli/create_api_key"},
		"billing":              {path: "/api/billing/prepaid"},
		"health, wrong host":   {path: front.HealthPath, method: http.MethodGet, host: "example.com"},
		"not the provider API": {path: "/metrics"},
		"nor its root":         {path: "/", method: http.MethodGet},
	} {
		resp, body, _ := g.do(c)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "looptender router front") {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, body)
		}
	}
	if n := len(g.up.Records()); n != 0 {
		t.Fatalf("%d refused requests reached the provider", n)
	}
	resp, body, _ := g.do(call{path: front.HealthPath, method: http.MethodGet, host: "localhost:" + g.addr[strings.LastIndex(g.addr, ":")+1:]})
	if resp.StatusCode != 200 || !strings.Contains(body, `"front":`+strconv.Itoa(front.Protocol)) {
		t.Fatalf("health: %d %q", resp.StatusCode, body)
	}
}

// A request whose length is stated twice is refused before it can be read
// two ways, and never reaches the provider.
func TestConflictingLengthsAreRefused(t *testing.T) {
	g := start(t, nil)
	conn, err := net.Dial("tcp4", g.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nContent-Length: 4\r\nContent-Length: 40\r\n\r\nabcdPOST /v1/messages HTTP/1.1\r\nHost: x\r\n\r\n", g.addr)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest || len(g.up.Records()) != 0 {
		t.Fatalf("status %d, %d requests reached the provider", resp.StatusCode, len(g.up.Records()))
	}
}

// The conversation is named by a header when the client sends one.
func TestConversationFromHeader(t *testing.T) {
	g := start(t, nil)
	g.ok(call{header: map[string]string{"X-Claude-Code-Session-Id": "from-the-header"}})
	if a := g.router.asked(); len(a) != 1 || a[0].Conversation != "from-the-header" {
		t.Fatalf("asked %+v", a)
	}
}

func TestNewNeedsAnUpstream(t *testing.T) {
	if _, err := front.New(front.Config{}, nil); err == nil {
		t.Fatal("a front without an upstream was accepted")
	}
	u, _ := url.Parse("ftp://example.com")
	if _, err := front.New(front.Config{Upstream: u}, nil); err == nil {
		t.Fatal("an ftp upstream was accepted")
	}
}

// The front is the one part in the path of every request, so it stays
// small: the standard library and nothing else.
func TestFrontImportsOnlyTheStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports %s", name, path)
			}
			switch path {
			// "log" is there only to hand the listener a logger that
			// writes nowhere; TestServerLogsNowhere checks that it does.
			case "os", "log/slog", "io/ioutil", "os/exec", "unsafe":
				t.Errorf("%s imports %s: the front reads no file, writes none and logs nothing", name, path)
			}
		}
	}
}

// A request with both a chunked body and a stated length cannot be read two
// ways by the provider: the front reads it as chunked, as the standard says,
// and sends on one request with one framing. What followed it on the
// connection is not a second request.
func TestChunkedWithALengthReachesTheProviderAsOneRequest(t *testing.T) {
	g := start(t, nil)
	conn, err := net.Dial("tcp4", g.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nConnection: close\r\nTransfer-Encoding: chunked\r\nContent-Length: 3\r\n\r\n5\r\nhello\r\n0\r\n\r\nGET /v1/smuggled HTTP/1.1\r\nHost: %s\r\n\r\n", g.addr, ownLogin, g.addr)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	recs := g.up.Records()
	if resp.StatusCode != 200 || len(recs) != 1 || recs[0].Path != "/v1/messages" || recs[0].BodyBytes != 5 {
		t.Fatalf("status %d, provider saw %v", resp.StatusCode, recs)
	}
	for _, h := range recs[0].Headers {
		if h == "Transfer-Encoding" {
			t.Fatal("the session's framing was forwarded")
		}
	}
}

// A session that stops sending its body is cut off after the bound, gets no
// answer that looks like success, and nothing reaches the provider.
func TestStalledBodyIsCutOff(t *testing.T) {
	g := start(t, func(c *front.Config) { c.BodyWait = 100 * time.Millisecond })
	conn, err := net.Dial("tcp4", g.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\n%s: %s\r\nContent-Length: 100\r\n\r\n{\"model\":", g.addr, front.SecretHeader, secret)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		t.Fatalf("a stalled request was answered with %d", resp.StatusCode)
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("the front kept a stalled request open past its bound")
	}
	if len(g.up.Records()) != 0 || len(g.router.asked()) != 0 {
		t.Fatal("a stalled request reached the provider or the router")
	}
}

type dialTimeout struct{}

func (dialTimeout) Error() string   { return "i/o timeout" }
func (dialTimeout) Timeout() bool   { return true }
func (dialTimeout) Temporary() bool { return true }

type cannotConnect struct{}

func (cannotConnect) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: dialTimeout{}}
}

// Not connecting in time is "could not be reached", not "did not answer
// within" the first-byte bound.
func TestConnectTimeoutIsNotAnAnswerTimeout(t *testing.T) {
	g := start(t, func(c *front.Config) { c.Transport = cannotConnect{} })
	resp, body, _ := g.do(call{})
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "could not be reached") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if o := g.router.outcome(t); o.Event != front.Unreachable {
		t.Fatalf("outcome %+v", o)
	}
}

// A routed request names the serving account, and only it: the account id
// inside user_id is replaced in place, the same length, and every other
// byte of the body is as the session sent it. A request on the session's
// own login is not touched at all.
func TestRoutedBodyNamesTheServingAccount(t *testing.T) {
	g := start(t, nil)
	g.router.set(always(to("spare", spareLogin)))
	g.ok(call{})
	g.router.set(always(front.Decision{Own: true}))
	g.ok(call{conversation: "22222222-2222-4333-8444-555555555555"})
	bodies, _ := g.sent()
	original := payloadFor(convA)
	routed := bodies[0]
	if len(routed) != len(original) || strings.Contains(routed, ownAccount) || strings.Count(routed, spareAccount) != 1 {
		t.Fatalf("routed body: len %d (sent %d), own account present %v, serving account %d times", len(routed), len(original), strings.Contains(routed, ownAccount), strings.Count(routed, spareAccount))
	}
	if strings.Replace(routed, spareAccount, ownAccount, 1) != original {
		t.Fatal("a byte other than the account id changed")
	}
	if bodies[1] != payloadFor("22222222-2222-4333-8444-555555555555") {
		t.Fatal("a request on the session's own login was changed")
	}
	if c := g.front.Counts(); c.Unroutable != 0 {
		t.Fatalf("counts %+v", c)
	}
}

// When the account a request names cannot be changed by one same-length
// replacement, the request is not routed: it goes on the session's own
// login, untouched, and is counted.
func TestRequestThatCannotBeRewrittenIsNotRouted(t *testing.T) {
	usual := payloadFor(convA)
	cases := map[string]struct {
		body     string
		decision front.Decision
	}{
		"no metadata":                     {body: `{"model":"model-large","messages":[]}`, decision: to("spare", spareLogin)},
		"not JSON":                        {body: `model-large, please`, decision: to("spare", spareLogin)},
		"user_id of another shape":        {body: `{"model":"model-large","metadata":{"user_id":"user_0123_account_` + ownAccount + `_session_` + convA + `"}}`, decision: to("spare", spareLogin)},
		"the account id also in a prompt": {body: strings.Replace(usual, "a {nested}", ownAccount+" {nested}", 1), decision: to("spare", spareLogin)},
		"no account id from the router":   {body: usual, decision: front.Decision{Subscription: "spare", Authorization: "Bearer " + spareLogin}},
		"an account id of another length": {body: usual, decision: front.Decision{Subscription: "spare", Authorization: "Bearer " + spareLogin, Account: "short"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := start(t, nil)
			g.router.set(always(c.decision))
			g.ok(call{body: c.body})
			bodies, _ := g.sent()
			if g.up.Served(spareLogin) != 0 || g.up.Served(ownLogin) != 1 || bodies[0] != c.body {
				t.Fatalf("spare %d, own %d, body changed %v", g.up.Served(spareLogin), g.up.Served(ownLogin), bodies[0] != c.body)
			}
			if n := g.front.Counts().Unroutable; n != 1 {
				t.Fatalf("counted %d", n)
			}
			if o := g.router.outcome(t); o.Subscription != "" || o.Status != 200 {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

// The conversation comes from the body when the client sends no header.
func TestConversationFromBody(t *testing.T) {
	g := start(t, nil)
	g.ok(call{})
	if a := g.router.asked(); len(a) != 1 || a[0].Conversation != convA || a[0].Model != "model-large" {
		t.Fatalf("asked %+v", a)
	}
}

// A long upload on a slow connection is not cut: the body bound is on
// silence, not on the whole upload.
func TestSlowUploadIsNotCut(t *testing.T) {
	g := start(t, func(c *front.Config) { c.BodyWait = 400 * time.Millisecond })
	conn, err := net.Dial("tcp4", g.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	body := payloadFor(convA)
	fmt.Fprintf(conn, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n%s: %s\r\nConnection: close\r\nContent-Length: %d\r\n\r\n", g.addr, ownLogin, front.SecretHeader, secret, len(body))
	began := time.Now()
	for i := 0; i < len(body); i += len(body)/8 + 1 {
		end := i + len(body)/8 + 1
		if end > len(body) {
			end = len(body)
		}
		io.WriteString(conn, body[i:end])
		time.Sleep(100 * time.Millisecond)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("after %v: %v %v", time.Since(began), resp, err)
	}
	if took := time.Since(began); took < 500*time.Millisecond {
		t.Fatalf("the upload took %v, not longer than the bound it is meant to outlast", took)
	}
	if bodies, _ := g.sent(); len(bodies) != 1 || bodies[0] != body {
		t.Fatal("the slow body did not arrive whole")
	}
}

// The Codex profile, on paper: a POST to /responses is routed with the
// login and the account header changed together and the body untouched;
// the conversation comes from a header; other paths go on unchanged or are
// refused. Nothing uses this profile yet.
func TestCodexProfile(t *testing.T) {
	g := start(t, func(c *front.Config) { c.Profile = front.Codex() })
	d := front.Decision{Subscription: "spare", Authorization: "Bearer " + spareLogin, Account: spareAccount}
	g.router.set(always(d))
	body := `{"model":"model-small","input":[],"prompt_cache_key":"k","stream":true}`
	headers := map[string]string{"Chatgpt-Account-Id": ownAccount, "Session-Id": convA, "Thread-Id": "33333333-2222-4333-8444-555555555555"}
	g.ok(call{path: "/responses", body: body, header: headers})
	g.ok(call{path: "/models", method: http.MethodGet, header: headers})
	bodies, sent := g.sent()
	if bodies[0] != body || sent[0].Get("Chatgpt-Account-Id") != spareAccount || sent[0].Get("Authorization") != "Bearer "+spareLogin || sent[0].Get("Session-Id") != convA {
		t.Fatalf("routed request: body changed %v, headers %v", bodies[0] != body, sent[0])
	}
	if sent[1].Get("Chatgpt-Account-Id") != ownAccount || sent[1].Get("Authorization") != "Bearer "+ownLogin {
		t.Fatal("a path that is not routed was changed")
	}
	if a := g.router.asked(); len(a) != 1 || a[0].Conversation != convA || a[0].Model != "model-small" {
		t.Fatalf("asked %+v", a)
	}
	if resp, _, _ := g.do(call{path: "/v1/messages"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("another provider's path: %d", resp.StatusCode)
	}
	// Without the serving account's id the request is not routed.
	g = start(t, func(c *front.Config) { c.Profile = front.Codex() })
	g.router.set(always(front.Decision{Subscription: "spare", Authorization: "Bearer " + spareLogin}))
	g.ok(call{path: "/responses", body: body, header: headers})
	if _, sent := g.sent(); sent[0].Get("Authorization") != "Bearer "+ownLogin || g.front.Counts().Unroutable != 1 {
		t.Fatal("a request was routed without the serving account's id")
	}
}

// Limit headers are reported by the profile's prefixes.
func TestLimitHeadersByProfile(t *testing.T) {
	g := start(t, func(c *front.Config) { c.Profile = front.Codex() })
	g.up.Script(ownLogin, standin.Step{Events: 1, Header: map[string]string{"X-Codex-Primary-Used-Percent": "40", "Anthropic-Ratelimit-Unified-Status": "allowed"}})
	g.router.set(always(front.Decision{Own: true}))
	g.ok(call{path: "/responses", body: `{"model":"model-small"}`})
	if o := g.router.outcome(t); o.Limits["x-codex-primary-used-percent"] != "40" || len(o.Limits) != 1 {
		t.Fatalf("limits %v", o.Limits)
	}
}
