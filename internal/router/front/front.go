// Package front is the only part of the account router that sits in the
// path of a request (docs/design/router.md, "The parts"). It forwards a
// request to one fixed provider address, asking a Decider which
// subscription should serve it and waiting a bounded time for the answer.
// With no answer, a slow answer or any failure, the request goes on
// unchanged, on the login the session sent with it.
//
// The front holds the connection to the provider and streams the answer
// itself, so nothing that happens to the Decider can cut a stream.
//
// It keeps nothing of a request or an answer. From a request body it takes
// the model name and the ids the provider's profile names and drops the
// rest. It changes a body in one case only: a routed request whose body
// names its account gets the serving account's id in place of its own, one
// value of the same length. To the Decider
// it reports the conversation, the model, the status and the provider's
// limit headers. It reads no file, writes none and logs nothing, and imports
// only the standard library; tests hold it to that.
package front

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Protocol is the number a front and a router must share. The front is
// replaced on an upgrade only when it changes.
const Protocol = 1

// SecretHeader carries the client secret from the session to the front. The
// front removes it before forwarding.
const SecretHeader = "X-Looptender-Client"

// HealthPath answers whether this is a front, and how many requests it has
// open.
const HealthPath = "/looptender/front"

// ErrNoRouter is what a Decider returns when nobody is there to ask, as
// opposed to an answer that came late. The front then stops asking for
// Config.NoRouterPause.
var ErrNoRouter = errors.New("no router is running")

// Ask is the one question the front has for the router.
type Ask struct {
	Client       string
	Conversation string
	Model        string
	// Exclude names the subscription that just refused this request, and
	// ExcludeOwn says the session's own login did.
	Exclude    []string
	ExcludeOwn bool
	// Retry marks the second question for one request.
	Retry bool
}

// Decision is the router's answer. Own means: forward unchanged. Otherwise
// Authorization replaces the request's own, for Subscription.
type Decision struct {
	Own           bool
	Subscription  string
	Authorization string
	// Account is the serving subscription's account id, for a provider
	// whose requests name the account. Like Authorization it is kept in
	// memory only and never reported.
	Account string
	// Expires is until when the front may reuse the answer for the same
	// conversation without asking. Zero: ask every time.
	Expires time.Time
}

// Events an Outcome can carry.
const (
	// StreamCut: the provider's answer ended before it was complete.
	StreamCut = "stream_cut"
	// UpstreamTimeout: the provider did not answer, or went silent, for
	// longer than the bound.
	UpstreamTimeout = "upstream_timeout"
	// Unreachable: no connection to the provider.
	Unreachable = "unreachable"
	// RetryExhausted: the one retry was also answered with a limit.
	RetryExhausted = "retry_exhausted"
)

// Outcome is everything the front tells the router about a request.
type Outcome struct {
	Client       string
	Conversation string
	Model        string
	// Subscription is empty when the session's own login was used.
	Subscription string
	Status       int
	RetryAfter   string
	// Limits are the provider's limit headers, by lower-case name.
	Limits       map[string]string
	Event        string
	RequestBytes int64
}

// Decider is the router as the front sees it. Decide must return when its
// context ends. Report must not block.
type Decider interface {
	Decide(context.Context, Ask) (Decision, error)
	Report(Outcome)
}

// Config is what a front needs to run.
type Config struct {
	// Upstream is the one address requests are forwarded to.
	Upstream *url.URL
	// Profile describes the provider behind Upstream. The zero value is
	// Claude Code's.
	Profile Profile
	// Port is the loopback port the front listens on; a request must name
	// it in Host.
	Port int
	// Client returns the client a secret was issued to.
	Client func(secret string) (client string, ok bool)
	// Transport replaces the connection to the provider, for tests.
	Transport http.RoundTripper
	Now       func() time.Time

	// MaxBody is the largest body kept in memory; a larger one is forwarded
	// unchanged and unrouted.
	MaxBody int64
	// FirstWait and MaxWait bound the wait for a decision; RetryWait bounds
	// the second question after a limit answer.
	FirstWait, MaxWait, RetryWait time.Duration
	// NoRouterPause is how long the front does not ask after ErrNoRouter.
	NoRouterPause time.Duration
	// BodyWait bounds silence while a request's body is read from the
	// session: a bound on a stalled upload, not on a long one. BodyTotal
	// bounds the whole upload, so a sender of one byte per interval does
	// not hold a request open for ever.
	BodyWait, BodyTotal time.Duration
	// ConnectWait, HeaderWait and IdleWait bound the provider: connecting,
	// the first byte of the answer, and silence inside a stream. HeaderWait
	// must not be shorter than the client's own request limit. IdleWait is
	// off unless set: a client was seen to wait four minutes on a silent
	// stream, and how often the provider sends something is not yet known.
	ConnectWait, HeaderWait, IdleWait time.Duration
	// MaxDecisions caps the decisions kept for reuse.
	MaxDecisions int
}

func (c *Config) defaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&c.FirstWait, 150*time.Millisecond)
	def(&c.MaxWait, 500*time.Millisecond)
	def(&c.RetryWait, 2*time.Second)
	def(&c.NoRouterPause, 5*time.Second)
	def(&c.BodyWait, 2*time.Minute)
	def(&c.BodyTotal, 15*time.Minute)
	def(&c.ConnectWait, 10*time.Second)
	// Above a client's own request limit, so the front never gives up on a
	// request its client would still have waited for.
	def(&c.HeaderWait, 15*time.Minute)
	if c.MaxBody <= 0 {
		c.MaxBody = 32 << 20
	}
	if c.MaxDecisions <= 0 {
		c.MaxDecisions = 4096
	}
	if c.Profile.Name == "" {
		c.Profile = ClaudeCode()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Client == nil {
		c.Client = func(string) (string, bool) { return "", false }
	}
	if c.Transport == nil {
		c.Transport = &http.Transport{
			Proxy:                 tunnelOnly,
			DialContext:           (&net.Dialer{Timeout: c.ConnectWait}).DialContext,
			TLSHandshakeTimeout:   c.ConnectWait,
			ResponseHeaderTimeout: c.HeaderWait,
			ForceAttemptHTTP2:     true,
			// The answer's bytes pass through as the provider sent them.
			DisableCompression: true,
		}
	}
}

// tunnelOnly uses the environment's proxy only for an https upstream, where
// the proxy carries an encrypted tunnel and sees no header. A plain http
// upstream is never reached through a proxy, which would see the login.
func tunnelOnly(r *http.Request) (*url.URL, error) {
	if r.URL.Scheme != "https" {
		return nil, nil
	}
	return http.ProxyFromEnvironment(r)
}

// Counts are the front's own tallies, for status.
type Counts struct {
	// Open is the number of requests in flight.
	Open int64
	// Missed counts decisions that did not arrive within the bound.
	Missed int64
	// Skipped counts requests not asked about because no router was there.
	Skipped int64
	// Unroutable counts requests the router named another subscription for
	// that went on their own login instead, because the account they name
	// could not be changed safely.
	Unroutable int64
	// Handshakes and HandshakesFailed count TLS connections that completed
	// their handshake and that did not. Failures with no successes mean
	// clients are pointed at the front and do not trust its certificate.
	Handshakes, HandshakesFailed int64
}

// Front is one running front.
type Front struct {
	cfg Config
	dec Decider
	srv *http.Server

	open, missed, skipped, unroutable atomic.Int64
	handshakes, handshakesFailed      atomic.Int64

	stopping chan struct{}
	stopOnce sync.Once

	mu         sync.Mutex
	decisions  map[string]Decision
	pauseUntil time.Time
	answerTime time.Duration // smoothed time the router takes to answer
}

// New returns a front that asks dec. Upstream must be set.
func New(cfg Config, dec Decider) (*Front, error) {
	if cfg.Upstream == nil || cfg.Upstream.Host == "" || (cfg.Upstream.Scheme != "https" && cfg.Upstream.Scheme != "http") {
		return nil, errors.New("front: an upstream address is required")
	}
	cfg.defaults()
	f := &Front{cfg: cfg, dec: dec, decisions: map[string]Decision{}, stopping: make(chan struct{})}
	f.srv = &http.Server{
		Handler:           f,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		// The standard library would otherwise write its own complaints
		// about a connection to standard error.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	return f, nil
}

// Serve answers requests on ln until Stop.
func (f *Front) Serve(ln net.Listener) error {
	if err := f.srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ServeTLS answers requests on ln over TLS with cert, the front's own
// loopback certificate, until Stop. It counts the handshakes that complete
// and those that fail.
func (f *Front) ServeTLS(ln net.Listener, cert tls.Certificate) error {
	return f.Serve(&handshaking{
		Listener: ln,
		front:    f,
		config:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ready:    make(chan net.Conn),
		done:     make(chan struct{}),
		slots:    make(chan struct{}, maxHandshakes),
	})
}

// maxHandshakes bounds the TLS handshakes in progress at once. A peer that
// connects and sends nothing holds one for ten seconds; beyond the bound a
// new connection is closed and counted as a failed handshake.
const maxHandshakes = 256

// handshaking is a listener that completes the TLS handshake itself, so it
// can count the ones that fail, and hands on only connections that
// completed it.
type handshaking struct {
	net.Listener
	front  *Front
	config *tls.Config
	once   sync.Once
	ready  chan net.Conn
	done   chan struct{}
	slots  chan struct{}
}

func (h *handshaking) Accept() (net.Conn, error) {
	h.once.Do(func() { go h.accept() })
	select {
	case c := <-h.ready:
		return c, nil
	case <-h.done:
		return nil, net.ErrClosed
	}
}

func (h *handshaking) accept() {
	var wait time.Duration
	for {
		c, err := h.Listener.Accept()
		if err != nil {
			// An error that passes (out of file descriptors, a connection
			// gone before it was accepted) must not end the front: wait a
			// little and go on, as net/http does for its own listeners.
			var ne net.Error
			if !errors.Is(err, net.ErrClosed) && errors.As(err, &ne) && (ne.Timeout() || temporaryAccept(err)) {
				if wait == 0 {
					wait = 5 * time.Millisecond
				} else if wait *= 2; wait > time.Second {
					wait = time.Second
				}
				select {
				case <-time.After(wait):
					continue
				case <-h.stopped():
				}
			}
			close(h.done)
			return
		}
		wait = 0
		select {
		case h.slots <- struct{}{}:
		default:
			h.front.handshakesFailed.Add(1)
			c.Close()
			continue
		}
		go func() {
			defer func() { <-h.slots }()
			t := tls.Server(c, h.config)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := t.HandshakeContext(ctx); err != nil {
				h.front.handshakesFailed.Add(1)
				c.Close()
				return
			}
			h.front.handshakes.Add(1)
			select {
			case h.ready <- t:
			case <-h.done:
				t.Close()
			}
		}()
	}
}

// stopped is closed when the front is told to stop.
func (h *handshaking) stopped() <-chan struct{} { return h.front.stopping }

// temporaryAccept reports whether an accept error is one that passes. The
// standard library marks these Temporary; the method is deprecated for
// errors in general but is still how accept errors are told apart.
func temporaryAccept(err error) bool {
	var t interface{ Temporary() bool }
	return errors.As(err, &t) && t.Temporary()
}

// Stop stops accepting and lets open requests finish until ctx ends. It
// returns how many were open when it was called and how many it cut.
func (f *Front) Stop(ctx context.Context) (open, cut int) {
	f.stopOnce.Do(func() { close(f.stopping) })
	open = int(f.open.Load())
	if err := f.srv.Shutdown(ctx); err != nil {
		cut = int(f.open.Load())
		f.srv.Close()
	}
	return open, cut
}

// Counts returns the front's tallies.
func (f *Front) Counts() Counts {
	return Counts{Open: f.open.Load(), Missed: f.missed.Load(), Skipped: f.skipped.Load(), Unroutable: f.unroutable.Load(), Handshakes: f.handshakes.Load(), HandshakesFailed: f.handshakesFailed.Load()}
}

// Replace gives the front a new decision for a conversation, when the
// router's answer changed. Forget drops the one it holds.
func (f *Front) Replace(client, conversation string, d Decision) {
	f.keep(key(client, conversation), d)
}

// Forget drops the decision held for a conversation.
func (f *Front) Forget(client, conversation string) {
	f.mu.Lock()
	delete(f.decisions, key(client, conversation))
	f.mu.Unlock()
}

func key(client, conversation string) string {
	if conversation == "" {
		return ""
	}
	return client + "\x00" + conversation
}

var own = Decision{Own: true}

func (f *Front) keep(k string, d Decision) {
	if k == "" || !d.Expires.After(f.cfg.Now()) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.decisions) >= f.cfg.MaxDecisions {
		now := f.cfg.Now()
		for old, v := range f.decisions {
			if !v.Expires.After(now) {
				delete(f.decisions, old)
			}
		}
		for old := range f.decisions {
			if len(f.decisions) < f.cfg.MaxDecisions {
				break
			}
			delete(f.decisions, old)
		}
	}
	f.decisions[k] = d
}

// held returns the decision kept for k, if it can still be used.
func (f *Front) held(k string) (Decision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decisions[k]
	if ok && !d.Expires.After(f.cfg.Now()) {
		delete(f.decisions, k)
		ok = false
	}
	return d, ok
}

// wait is how long the first question may take now: several times what the
// router has been taking, within FirstWait and MaxWait.
func (f *Front) wait() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := 4 * f.answerTime
	if w < f.cfg.FirstWait {
		w = f.cfg.FirstWait
	}
	if w > f.cfg.MaxWait {
		w = f.cfg.MaxWait
	}
	return w
}

// ask puts one question to the router and waits at most wait. Every failure
// is the answer "your own login".
func (f *Front) ask(ctx context.Context, a Ask, wait time.Duration) Decision {
	now := f.cfg.Now()
	f.mu.Lock()
	paused := now.Before(f.pauseUntil)
	f.mu.Unlock()
	if paused || f.dec == nil {
		f.skipped.Add(1)
		return own
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	type answer struct {
		d   Decision
		err error
	}
	ch := make(chan answer, 1)
	start := time.Now()
	go func() {
		d, err := f.dec.Decide(ctx, a)
		ch <- answer{d, err}
	}()
	var got answer
	select {
	case got = <-ch:
	case <-ctx.Done():
		got.err = ctx.Err()
	}
	took := time.Since(start)
	f.mu.Lock()
	switch {
	case errors.Is(got.err, ErrNoRouter):
		f.pauseUntil = now.Add(f.cfg.NoRouterPause)
	default:
		// A slow router is asked again, with a longer wait.
		f.answerTime = (3*f.answerTime + took) / 4
	}
	f.mu.Unlock()
	if got.err != nil {
		if errors.Is(got.err, ErrNoRouter) {
			f.skipped.Add(1)
		} else {
			f.missed.Add(1)
		}
		return own
	}
	if !got.d.Own && (got.d.Authorization == "" || got.d.Subscription == "") {
		return own
	}
	f.keep(key(a.Client, a.Conversation), got.d)
	return got.d
}

func (f *Front) hostOK(host string) bool {
	port := strconv.Itoa(f.cfg.Port)
	return host == "127.0.0.1:"+port || host == "localhost:"+port
}

// say writes one of the few answers that are the front's own.
func say(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "5")
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, "looptender router front: "+text)
}

// ServeHTTP forwards one request.
func (f *Front) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.hostOK(r.Host) || r.Header.Get("Origin") != "" {
		say(w, http.StatusForbidden, "not a request for this front")
		return
	}
	if r.URL.Path == HealthPath {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"front\":%d,\"open\":%d}\n", Protocol, f.open.Load())
		return
	}
	p := f.cfg.Profile
	if !p.forwards(r.URL.Path) {
		say(w, http.StatusForbidden, "this path is not forwarded")
		return
	}
	f.open.Add(1)
	defer f.open.Add(-1)

	secret := r.Header.Get(SecretHeader)
	r.Header.Del(SecretHeader)
	client, known := "", false
	if secret != "" {
		client, known = f.cfg.Client(secret)
	}

	// Anything that is not a known client's request on a routed path goes
	// through unchanged, body unread.
	if !known || r.Method != http.MethodPost || !p.Routed[r.URL.Path] {
		f.finish(w, r, r.Body, r.ContentLength, Outcome{Client: client}, own)
		return
	}
	// A session that stops sending its body does not keep a request open;
	// one that is still sending is given all the time it needs.
	reading := http.NewResponseController(w)
	latest := time.Now().Add(f.cfg.BodyTotal)
	body, whole, err := readUpTo(r.Body, f.cfg.MaxBody, func() {
		until := time.Now().Add(f.cfg.BodyWait)
		if until.After(latest) {
			until = latest
		}
		reading.SetReadDeadline(until)
	})
	if err != nil {
		// The session went away or stalled mid-body. There is nothing to
		// answer, and returning would send an empty 200.
		panic(http.ErrAbortHandler)
	}
	reading.SetReadDeadline(time.Time{})
	if !whole {
		rest := io.MultiReader(bytes.NewReader(body), r.Body)
		f.finish(w, r, rest, r.ContentLength, Outcome{Client: client}, own)
		return
	}
	var model, conversation string
	if p.Body != nil {
		model, conversation = p.Body(body)
	}
	if h := r.Header.Get(p.ConversationHeader); p.ConversationHeader != "" && h != "" {
		conversation = h
	}
	o := Outcome{Client: client, Conversation: conversation, Model: model, RequestBytes: int64(len(body))}
	a := Ask{Client: client, Conversation: conversation, Model: model}

	d, ok := f.held(key(client, conversation))
	if !ok {
		d = f.ask(r.Context(), a, f.wait())
	}
	sent, d := f.shaped(body, d)
	resp, err := f.send(r.Context(), r, bytes.NewReader(sent), int64(len(sent)), d)
	if err != nil {
		f.failed(w, r, o, d, err)
		return
	}
	// A limit answer, or a rejected login that is not the session's own,
	// means the provider did not run the request: it may be sent once more.
	// A rejection of the session's own login is the client's to handle.
	limit := resp.StatusCode == http.StatusTooManyRequests
	if limit || (resp.StatusCode == http.StatusUnauthorized && !d.Own) {
		f.Forget(client, conversation)
		next := own
		if limit {
			a.Retry, a.ExcludeOwn = true, d.Own
			if !d.Own {
				a.Exclude = []string{d.Subscription}
			}
			next = f.ask(r.Context(), a, f.cfg.RetryWait)
		}
		if !(next.Own && d.Own) {
			f.report(o, d, resp, "")
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			sent, d = f.shaped(body, next)
			if resp, err = f.send(r.Context(), r, bytes.NewReader(sent), int64(len(sent)), d); err != nil {
				f.failed(w, r, o, d, err)
				return
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				o.Event = RetryExhausted
			}
		}
	}
	f.stream(w, r, resp, o, d)
}

// shaped returns the body to send for decision d, and d. When d names
// another subscription but the request cannot be made to name that
// subscription's account and nothing else, the request is not routed: the
// body comes back unchanged with the decision "your own login".
func (f *Front) shaped(body []byte, d Decision) ([]byte, Decision) {
	p := f.cfg.Profile
	if d.Own || (p.Rewrite == nil && p.AccountHeader == "") {
		return body, d
	}
	// What goes into a request as an account id is a UUID or nothing.
	if !accountID.MatchString(d.Account) {
		f.unroutable.Add(1)
		return body, own
	}
	if p.Rewrite == nil {
		return body, d
	}
	changed, ok := p.Rewrite(body, d.Account)
	if !ok {
		f.unroutable.Add(1)
		return body, own
	}
	return changed, d
}

// finish sends a request that is not routed and streams its answer.
func (f *Front) finish(w http.ResponseWriter, r *http.Request, body io.Reader, n int64, o Outcome, d Decision) {
	resp, err := f.send(r.Context(), r, body, n, d)
	if err != nil {
		f.failed(w, r, o, d, err)
		return
	}
	f.stream(w, r, resp, o, d)
}

// failed answers a request the provider never answered.
func (f *Front) failed(w http.ResponseWriter, r *http.Request, o Outcome, d Decision, err error) {
	if r.Context().Err() != nil {
		return // the client went away
	}
	var op *net.OpError
	var ne net.Error
	if errors.As(err, &op) && op.Op == "dial" {
		// Not connecting in time is not the provider failing to answer.
	} else if errors.As(err, &ne) && ne.Timeout() {
		o.Event = UpstreamTimeout
		f.report(o, d, nil, o.Event)
		say(w, http.StatusGatewayTimeout, "the provider did not answer within "+f.cfg.HeaderWait.String())
		return
	}
	f.report(o, d, nil, Unreachable)
	say(w, http.StatusBadGateway, "the provider could not be reached")
}

// Headers that belong to one connection and are never forwarded.
var hopByHop = []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// copyHeaders copies src to dst without the headers of one connection,
// those the Connection header names, any Proxy- header and the client
// secret.
func copyHeaders(dst, src http.Header) {
	drop := map[string]bool{SecretHeader: true}
	for _, h := range hopByHop {
		drop[h] = true
	}
	for _, v := range src.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			drop[http.CanonicalHeaderKey(strings.TrimSpace(name))] = true
		}
	}
	for name, values := range src {
		if drop[name] || strings.HasPrefix(name, "Proxy-") {
			continue
		}
		dst[name] = append([]string(nil), values...)
	}
}

// send forwards the request to the fixed upstream with d's login, or its
// own.
func (f *Front) send(ctx context.Context, r *http.Request, body io.Reader, n int64, d Decision) (*http.Response, error) {
	// The path goes on as it was written: an escaped slash stays escaped.
	up := f.cfg.Upstream
	target := up.Scheme + "://" + up.Host + strings.TrimSuffix(up.EscapedPath(), "/") + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	out, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		return nil, err
	}
	out.ContentLength = n
	if n == 0 {
		out.Body = http.NoBody
	}
	copyHeaders(out.Header, r.Header)
	out.Host = f.cfg.Upstream.Host
	if !d.Own {
		out.Header.Set("Authorization", d.Authorization)
		for _, name := range f.cfg.Profile.DropWithLogin {
			out.Header.Del(name)
		}
		if name := f.cfg.Profile.AccountHeader; name != "" {
			out.Header.Set(name, d.Account)
		}
	}
	return f.cfg.Transport.RoundTrip(out)
}

// report tells the router how a request went. It never waits for it.
func (f *Front) report(o Outcome, d Decision, resp *http.Response, event string) {
	if f.dec == nil {
		return
	}
	if !d.Own {
		o.Subscription = d.Subscription
	}
	if event != "" {
		o.Event = event
	}
	if resp != nil {
		o.Status = resp.StatusCode
		o.RetryAfter = resp.Header.Get("Retry-After")
		for name, values := range resp.Header {
			if l := strings.ToLower(name); f.cfg.Profile.limit(l) && len(values) > 0 {
				if o.Limits == nil {
					o.Limits = map[string]string{}
				}
				o.Limits[l] = values[0]
			}
		}
	}
	go f.dec.Report(o)
}

// stream passes the provider's answer to the session. An answer that ends
// early or goes silent is never ended cleanly: the connection is broken, so
// the session sees a failed request and not a complete-looking answer.
func (f *Front) stream(w http.ResponseWriter, r *http.Request, resp *http.Response, o Outcome, d Decision) {
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flush := http.NewResponseController(w)
	flush.Flush()

	var silent atomic.Bool
	heard := func() {}
	if f.cfg.IdleWait > 0 {
		idle := time.AfterFunc(f.cfg.IdleWait, func() {
			silent.Store(true)
			resp.Body.Close()
		})
		defer idle.Stop()
		heard = func() { idle.Reset(f.cfg.IdleWait) }
	}

	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			heard()
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // the client went away
			}
			flush.Flush()
		}
		if err == io.EOF {
			f.report(o, d, resp, "")
			return
		}
		if err != nil {
			if r.Context().Err() != nil {
				return // the client went away
			}
			event := StreamCut
			if silent.Load() {
				event = UpstreamTimeout
			}
			f.report(o, d, resp, event)
			panic(http.ErrAbortHandler)
		}
	}
}

// readUpTo reads at most max bytes of r, calling waiting before each read so
// the caller can bound silence rather than the whole read. whole is false
// when more follow.
func readUpTo(r io.Reader, max int64, waiting func()) (body []byte, whole bool, err error) {
	buf := make([]byte, 32<<10)
	for {
		waiting()
		n, err := r.Read(buf)
		body = append(body, buf[:n]...)
		if int64(len(body)) > max {
			return body, false, nil
		}
		if err == io.EOF {
			return body, true, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
}

// skip reads past one JSON value.
func skip(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}
