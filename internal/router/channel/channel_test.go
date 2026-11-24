//go:build unix

package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/router/core"
	"github.com/looptender/looptender/internal/router/front"
	"github.com/looptender/looptender/internal/router/standin"
)

// privateDir is a short private directory: a socket's path has a small
// limit.
func privateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

type handler struct {
	mu       sync.Mutex
	decide   func(context.Context, front.Ask) (front.Decision, error)
	outcomes chan front.Outcome
}

func (h *handler) Decide(ctx context.Context, a front.Ask) (front.Decision, error) {
	h.mu.Lock()
	decide := h.decide
	h.mu.Unlock()
	return decide(ctx, a)
}
func (h *handler) Report(o front.Outcome) { h.outcomes <- o }

func serve(t *testing.T, dir string, h Handler) *Server {
	t.Helper()
	s, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(h)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDecideAndReport(t *testing.T) {
	dir := privateDir(t)
	want := front.Decision{Subscription: "spare", Authorization: "Bearer made-up", Account: "made-up-account", Expires: time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)}
	asks := make(chan front.Ask, 1)
	h := &handler{outcomes: make(chan front.Outcome, 1), decide: func(_ context.Context, a front.Ask) (front.Decision, error) {
		asks <- a
		return want, nil
	}}
	serve(t, dir, h)
	c := NewClient(dir)
	ask := front.Ask{Client: "client-one", Conversation: "c1", Model: "model-large", Exclude: []string{"main"}, ExcludeOwn: true, Retry: true}
	got, err := c.Decide(context.Background(), ask)
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	asked := <-asks
	if asked.Client != ask.Client || asked.Conversation != ask.Conversation || asked.Model != ask.Model || !asked.Retry || !asked.ExcludeOwn || len(asked.Exclude) != 1 {
		t.Fatalf("the router was asked %+v", asked)
	}
	c.Report(front.Outcome{Client: "client-one", Subscription: "spare", Status: 429, RetryAfter: "60", Limits: map[string]string{"a": "b"}, Event: front.RetryExhausted})
	select {
	case o := <-h.outcomes:
		if o.Subscription != "spare" || o.Status != 429 || o.RetryAfter != "60" || o.Limits["a"] != "b" || o.Event != front.RetryExhausted {
			t.Fatalf("outcome %+v", o)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the outcome did not arrive")
	}
	// The socket is closed to everyone but the user.
	if st, err := os.Stat(filepath.Join(dir, SocketName)); err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("socket mode %v %v", st.Mode(), err)
	}
}

// No router, a router that went away, and a router of another protocol are
// all "nobody to ask", which the front answers by forwarding on the
// session's own login.
func TestNoRouter(t *testing.T) {
	dir := privateDir(t)
	c := NewClient(dir)
	if _, err := c.Decide(context.Background(), front.Ask{}); !errors.Is(err, front.ErrNoRouter) {
		t.Fatalf("with no socket: %v", err)
	}
	c.Report(front.Outcome{}) // says nothing, does not wait
	s := serve(t, dir, &handler{decide: func(context.Context, front.Ask) (front.Decision, error) { return front.Decision{Own: true}, nil }})
	if _, err := c.Decide(context.Background(), front.Ask{}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := c.Decide(context.Background(), front.Ask{}); !errors.Is(err, front.ErrNoRouter) {
		t.Fatalf("after the router stopped: %v", err)
	}
}

func TestOtherProtocolIsRefused(t *testing.T) {
	dir := privateDir(t)
	var called atomic.Bool
	serve(t, dir, &handler{decide: func(context.Context, front.Ask) (front.Decision, error) {
		called.Store(true)
		return front.Decision{Subscription: "spare", Authorization: "Bearer made-up"}, nil
	}})
	conn, err := net.Dial("unix", filepath.Join(dir, SocketName))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	b, _ := json.Marshal(request{Protocol: front.Protocol + 1, Op: "decide", Ask: &front.Ask{}})
	conn.Write(append(b, '\n'))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if called.Load() || !strings.Contains(line, `"refused":"protocol"`) || strings.Contains(line, "made-up") {
		t.Fatalf("called=%v reply %q", called.Load(), line)
	}
	// Garbage and an unknown operation get no decision either.
	for _, raw := range []string{"not json\n", `{"protocol":1,"op":"shutdown"}` + "\n"} {
		conn, _ := net.Dial("unix", filepath.Join(dir, SocketName))
		conn.Write([]byte(raw))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		if strings.Contains(line, "decision") {
			t.Fatalf("%q was answered with %q", raw, line)
		}
	}
}

// A router that answers late does not hold the front: the wait is the
// caller's, and a late answer is not "no router".
func TestSlowRouterIsNotNoRouter(t *testing.T) {
	dir := privateDir(t)
	release := make(chan struct{})
	serve(t, dir, &handler{decide: func(ctx context.Context, _ front.Ask) (front.Decision, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return front.Decision{Own: true}, nil
	}})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := NewClient(dir).Decide(ctx, front.Ask{})
	if err == nil || errors.Is(err, front.ErrNoRouter) || time.Since(began) > 3*time.Second {
		t.Fatalf("after %v: %v", time.Since(began), err)
	}
}

func TestListenNeedsAPrivateDirectory(t *testing.T) {
	dir := privateDir(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(dir); !errors.Is(err, ErrDirectory) {
		t.Fatalf("a directory others can enter: %v", err)
	}
	if _, err := Listen(filepath.Join(dir, "missing")); !errors.Is(err, ErrDirectory) {
		t.Fatalf("a missing directory: %v", err)
	}
	link := filepath.Join(privateDir(t), "link")
	os.Chmod(dir, 0o700)
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(link); !errors.Is(err, ErrDirectory) {
		t.Fatalf("a link to a directory: %v", err)
	}
}

func TestOneRouterPerDirectory(t *testing.T) {
	dir := privateDir(t)
	h := &handler{decide: func(context.Context, front.Ask) (front.Decision, error) { return front.Decision{Own: true}, nil }}
	serve(t, dir, h)
	if _, err := Listen(dir); !errors.Is(err, ErrRunning) {
		t.Fatalf("a second router: %v", err)
	}
	// A socket file left by a router that is gone is cleared away.
	stale := privateDir(t)
	ln, err := net.Listen("unix", filepath.Join(stale, SocketName))
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	s, err := Listen(stale)
	if err != nil {
		t.Fatalf("over a stale socket: %v", err)
	}
	s.Close()
}

// The whole path with real pieces and made-up logins: a session whose own
// subscription is at its limit is served by another, through the front, the
// channel and the router; when the router stops, requests go on unchanged.
func TestFrontChannelAndRouterTogether(t *testing.T) {
	const ownLogin, spareLogin = "sk-ant-oat01-own-login", "sk-ant-oat01-spare-login"
	const ownAccount, spareAccount = "99999999-2222-4333-8444-555555555555", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	up := &standin.Upstream{}
	up.Script(ownLogin, standin.Step{Status: 429, Header: map[string]string{"Retry-After": "600"}, Body: `{"own":"limit"}`})
	var mu sync.Mutex
	var bodies []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		bufio.NewReader(r.Body).WriteTo(buf)
		mu.Lock()
		bodies = append(bodies, buf.String())
		mu.Unlock()
		r.Body = http.NoBody
		up.ServeHTTP(w, r)
	}))
	defer provider.Close()
	u, _ := url.Parse(provider.URL)

	dir := privateDir(t)
	rt := core.New(core.Config{
		Order:  []string{"main", "spare"},
		Home:   map[string]string{"client-one": "main"},
		Logins: fixed{"spare": core.Login{Authorization: "Bearer " + spareLogin, Account: spareAccount, Expires: time.Now().Add(time.Hour)}},
	})
	srv := serve(t, dir, rt)

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f, err := front.New(front.Config{Upstream: u, Port: ln.Addr().(*net.TCPAddr).Port, Client: func(s string) (string, bool) { return "client-one", s == "client-secret" }}, NewClient(dir))
	if err != nil {
		t.Fatal(err)
	}
	go f.Serve(ln)
	defer f.Stop(context.Background())

	body := `{"model":"model-large","messages":[],"metadata":{"user_id":"{\"device_id\":\"abab\",\"account_uuid\":\"` + ownAccount + `\",\"session_id\":\"11111111-2222-4333-8444-555555555555\"}"}}`
	send := func() int {
		req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/v1/messages", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ownLogin)
		req.Header.Set(front.SecretHeader, "client-secret")
		resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		bufio.NewReader(resp.Body).WriteTo(new(strings.Builder))
		return resp.StatusCode
	}
	// The own login is refused with a limit; the same request succeeds on
	// the spare, naming the spare's account.
	if code := send(); code != 200 {
		t.Fatalf("status %d", code)
	}
	if up.Served(ownLogin) != 1 || up.Served(spareLogin) != 1 {
		t.Fatalf("own %d, spare %d", up.Served(ownLogin), up.Served(spareLogin))
	}
	mu.Lock()
	routed := bodies[1]
	mu.Unlock()
	if strings.Contains(routed, ownAccount) || strings.Count(routed, spareAccount) != 1 || len(routed) != len(body) {
		t.Fatal("the routed request does not name exactly the serving account")
	}
	// The router now knows the home subscription is held: the next request
	// goes straight to the spare.
	deadline := time.Now().Add(5 * time.Second)
	for rt.Counts().Routed < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // the first request's reports
	if code := send(); code != 200 || up.Served(ownLogin) != 1 || up.Served(spareLogin) != 2 {
		t.Fatalf("second request: status %d, own %d, spare %d", code, up.Served(ownLogin), up.Served(spareLogin))
	}
	// The router stops. A conversation the front holds a decision for goes
	// on as decided; a new one goes on its own login, unchanged, and gets
	// the provider's own answer.
	srv.Close()
	f.Forget("client-one", "11111111-2222-4333-8444-555555555555")
	if code := send(); code != 429 || up.Served(ownLogin) != 2 {
		t.Fatalf("with the router gone: status %d, own %d", code, up.Served(ownLogin))
	}
}

type fixed map[string]core.Login

func (f fixed) Login(_ context.Context, sub string) (core.Login, error) {
	l, ok := f[sub]
	if !ok {
		return core.Login{}, errors.New("unavailable")
	}
	return l, nil
}

// squatter is a listener that is not the router: it answers every
// connection with a decision naming another login, and counts the requests
// it was sent.
type squatter struct {
	ln       net.Listener
	mu       sync.Mutex
	requests int
}

func squat(t *testing.T, dir string) *squatter {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(dir, SocketName))
	if err != nil {
		t.Fatal(err)
	}
	s := &squatter{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				if line, _ := bufio.NewReader(c).ReadString('\n'); line != "" {
					s.mu.Lock()
					s.requests++
					s.mu.Unlock()
				}
				c.Write([]byte(`{"decision":{"Subscription":"theirs","Authorization":"Bearer someone-elses-login","Account":"someone-elses-account"}}` + "\n"))
			}()
		}
	}()
	return s
}

func (s *squatter) got() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// The front asks only its own user's router. A socket in a directory that
// is not the user's alone is not asked at all, whoever listens there: the
// answer is "no router", never a decision, and no question is sent.
func TestClientTrustsOnlyAPrivateDirectory(t *testing.T) {
	shared := privateDir(t)
	s := squat(t, shared)
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := NewClient(shared).Decide(context.Background(), front.Ask{Client: "client-one"})
	if !errors.Is(err, front.ErrNoRouter) || d.Authorization != "" || s.got() != 0 {
		t.Fatalf("a directory others can enter: %+v %v, squatter was sent %d requests", d, err, s.got())
	}
	// A link to a private directory is not a private directory.
	private := privateDir(t)
	s2 := squat(t, private)
	link := filepath.Join(privateDir(t), "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(link).Decide(context.Background(), front.Ask{}); !errors.Is(err, front.ErrNoRouter) || s2.got() != 0 {
		t.Fatalf("through a link: %v, squatter was sent %d", err, s2.got())
	}
	// The same listener in the private directory itself is another user's
	// only if the system says so: here it is this user, so it is asked. This
	// is the control for the two refusals above.
	if d, err := NewClient(private).Decide(context.Background(), front.Ask{}); err != nil || d.Subscription != "theirs" || s2.got() != 1 {
		t.Fatalf("the control: %+v %v", d, err)
	}
}

// Each end asks the system who is at the other end and deals only with its
// own user. Here the identity is replaced: first what the system says of
// the peer, then who this process is.
func TestPeerOfAnotherUserIsRefused(t *testing.T) {
	realOwn := os.Getuid
	// as replaces, under the lock both ends read it with, what the system
	// says of a peer and who this process is.
	as := func(peer func(*net.UnixConn) (int, bool), own func() int) {
		identity.Lock()
		identity.peer, identity.own = peer, own
		identity.Unlock()
	}
	defer as(systemPeerUID, realOwn)
	other := func(*net.UnixConn) (int, bool) { return realOwn() + 1, true }
	unknown := func(*net.UnixConn) (int, bool) { return 0, false }

	// The router does not answer another user, or a peer it cannot identify.
	dir := privateDir(t)
	var called atomic.Int32
	serve(t, dir, &handler{decide: func(context.Context, front.Ask) (front.Decision, error) {
		called.Add(1)
		return front.Decision{Subscription: "spare", Authorization: "Bearer made-up"}, nil
	}})
	raw := func() string {
		conn, err := net.Dial("unix", filepath.Join(dir, SocketName))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		b, _ := json.Marshal(request{Protocol: front.Protocol, Op: "decide", Ask: &front.Ask{}})
		conn.Write(append(b, '\n'))
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		return line
	}
	if line := raw(); !strings.Contains(line, "made-up") {
		t.Fatalf("the control (the same user) was not answered: %q", line)
	}
	for name, who := range map[string]func(*net.UnixConn) (int, bool){"another user": other, "a peer the system cannot name": unknown} {
		as(who, realOwn)
		if line := raw(); line != "" || called.Load() != 1 {
			t.Fatalf("%s was answered %q (handler called %d times)", name, line, called.Load())
		}
	}
	as(systemPeerUID, realOwn)

	// The front does not ask a router that is another user's, and sends it
	// nothing.
	theirs := privateDir(t)
	s := squat(t, theirs)
	for name, who := range map[string]func(*net.UnixConn) (int, bool){"another user": other, "a peer the system cannot name": unknown} {
		as(who, realOwn)
		d, err := NewClient(theirs).Decide(context.Background(), front.Ask{Client: "client-one"})
		if !errors.Is(err, front.ErrNoRouter) || d.Authorization != "" || s.got() != 0 {
			t.Fatalf("%s: %+v %v, sent %d requests", name, d, err, s.got())
		}
	}
	// A directory that belongs to another user is not this user's, however
	// closed it is.
	as(systemPeerUID, func() int { return realOwn() + 1 })
	if _, err := CheckDirectory(theirs); !errors.Is(err, ErrDirectory) {
		t.Fatalf("a directory of another user: %v", err)
	}
	if _, err := Listen(theirs); !errors.Is(err, ErrDirectory) {
		t.Fatalf("listening in a directory of another user: %v", err)
	}
}

// A router of another protocol is, for the front, no router.
func TestOtherProtocolIsNoRouter(t *testing.T) {
	dir := privateDir(t)
	serve(t, dir, &handler{decide: func(context.Context, front.Ask) (front.Decision, error) {
		return front.Decision{Subscription: "spare", Authorization: "Bearer made-up"}, nil
	}})
	defer func(p int) { protocol = p }(protocol)
	protocol = front.Protocol + 1
	if d, err := NewClient(dir).Decide(context.Background(), front.Ask{}); !errors.Is(err, front.ErrNoRouter) || d.Authorization != "" {
		t.Fatalf("%+v %v", d, err)
	}
}

// Only "nobody is there" is no router. Running out of descriptors, or a
// full backlog, is a router that is there: the front must go on asking.
func TestAbsent(t *testing.T) {
	for _, e := range []error{syscall.ENOENT, syscall.ECONNREFUSED, &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}} {
		if !absent(e) {
			t.Errorf("%v is not taken as no router", e)
		}
	}
	for _, e := range []error{syscall.EAGAIN, syscall.EMFILE, syscall.ENFILE, syscall.EACCES, errors.New("something else"), &net.OpError{Op: "dial", Err: os.NewSyscallError("socket", syscall.EMFILE)}} {
		if absent(e) {
			t.Errorf("%v is taken as no router", e)
		}
	}
}

// The router goes on serving through accept errors that pass, and stops for
// one that does not.
func TestServeSurvivesPassingAcceptErrors(t *testing.T) {
	dir := privateDir(t)
	s, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Set before Serve starts and never replaced: the first three accepts
	// fail with an error that passes; once broken is set, every accept fails
	// with one that does not.
	var fails atomic.Int32
	var broken atomic.Bool
	fails.Store(3)
	real := s.accept
	s.accept = func() (*net.UnixConn, error) {
		if fails.Add(-1) >= 0 {
			return nil, &net.OpError{Op: "accept", Net: "unix", Err: os.NewSyscallError("accept", syscall.EMFILE)}
		}
		c, err := real()
		if broken.Load() {
			if c != nil {
				c.Close()
			}
			return nil, errors.New("the listener is broken")
		}
		return c, err
	}
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(&handler{decide: func(context.Context, front.Ask) (front.Decision, error) { return front.Decision{Own: true}, nil }})
	}()
	if d, err := NewClient(dir).Decide(context.Background(), front.Ask{}); err != nil || !d.Own {
		t.Fatalf("after three passing errors: %+v %v", d, err)
	}
	select {
	case err := <-done:
		t.Fatalf("the router stopped: %v", err)
	default:
	}
	broken.Store(true)
	// Wake the accept that is waiting in the real listener.
	if c, err := net.Dial("unix", filepath.Join(dir, SocketName)); err == nil {
		c.Close()
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an error that does not pass ended the router without saying so")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an error that does not pass did not end the router")
	}
}

// Peers that connect and say nothing do not use the router up: beyond a
// bound a new connection is closed unanswered, and when they leave it
// answers again.
func TestSilentPeersAreBounded(t *testing.T) {
	dir := privateDir(t)
	serve(t, dir, &handler{decide: func(context.Context, front.Ask) (front.Decision, error) { return front.Decision{Own: true}, nil }})
	var silent []net.Conn
	for i := 0; i < maxPeers; i++ {
		c, err := net.Dial("unix", filepath.Join(dir, SocketName))
		if err != nil {
			t.Fatal(err)
		}
		silent = append(silent, c)
	}
	time.Sleep(200 * time.Millisecond) // let the router take them all in
	// One more is closed at once, not left waiting for a place.
	extra, err := net.Dial("unix", filepath.Join(dir, SocketName))
	if err != nil {
		t.Fatal(err)
	}
	extra.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the connection beyond the bound was kept open: %v", err)
	}
	extra.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err = NewClient(dir).Decide(ctx, front.Ask{})
	cancel()
	if err == nil || errors.Is(err, front.ErrNoRouter) {
		t.Fatalf("with every place taken: %v (a full router is not an absent one)", err)
	}
	for _, c := range silent {
		c.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if d, err := NewClient(dir).Decide(context.Background(), front.Ask{}); err == nil && d.Own {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the router did not answer again after the silent peers left")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The directory is still the one that was checked when the socket is bound
// in it. Its parents are not walked; a directory put in its place between
// the check and the bind is caught here instead.
func TestDirectoryReplacedBeforeBind(t *testing.T) {
	parent := privateDir(t)
	dir := filepath.Join(parent, "router")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { beforeBind = func() {} }()
	beforeBind = func() {
		if err := os.Rename(dir, filepath.Join(parent, "moved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Listen(dir); !errors.Is(err, ErrDirectory) {
		t.Fatalf("a directory replaced between the check and the bind: %v", err)
	}
}
