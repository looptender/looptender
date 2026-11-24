// Package standin is a stand-in for a provider's API, for testing the
// account router's front without a provider or a real login
// (docs/design/router.md, "What must be verified").
//
// It answers each request as a script says, chosen by the credential the
// request carries, and it records what arrived. A record holds the names of
// the request's headers and the shape of its credential: its kind, the
// length of its prefix and its length. It never holds a header's value, a
// body or a credential, so it stays safe to read when a real login is on
// the wire. Scripts and counts are kept under a digest of the credential
// they are for, keyed with a random value made for each Upstream and never
// shown, so the digest says nothing outside the process.
package standin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Step is how one request is answered.
type Step struct {
	// Status is the answer's status; 0 means 200.
	Status int
	// Header is added to the answer.
	Header map[string]string
	// Body answers a request whose Status is not 200.
	Body string
	// Events is how many stream events a 200 sends, Gap apart.
	Events int
	Gap    time.Duration
	// CutAfter, when positive, drops the connection after that many events.
	CutAfter int
	// SilentAfter, when positive, sends that many events and then nothing,
	// keeping the connection open until the client leaves.
	SilentAfter int
	// Hang sends no answer at all until the client leaves.
	Hang bool
}

// Record is what is kept of one request.
type Record struct {
	Method string
	// Path is the path as it was written, escapes kept.
	Path string
	// Headers are the request's header names, sorted. No values.
	Headers []string
	Host    string
	// Credential describes the login the request carried.
	Credential Shape
	BodyBytes  int64
}

// Shape describes a credential without holding it.
type Shape struct {
	// Carrier is "authorization", "x-api-key" or "" when none was sent.
	Carrier string
	// Scheme is "bearer" or "basic", or "other" for anything else,
	// including a value with no scheme at all.
	Scheme string
	// Kind is "oauth_access", "api_key" or "other", told from the
	// provider's public prefixes.
	Kind string
	// PrefixLen is the length of the leading part up to and including the
	// third "-", 0 when there is none.
	PrefixLen int
	Len       int
}

// ShapeOf describes the credential in h.
func ShapeOf(h http.Header) Shape {
	s := Shape{}
	v := ""
	if a := h.Get("Authorization"); a != "" {
		s.Carrier = "authorization"
		s.Scheme, v = "other", a
		if scheme, rest, ok := strings.Cut(a, " "); ok {
			if l := strings.ToLower(scheme); l == "bearer" || l == "basic" {
				s.Scheme, v = l, strings.TrimSpace(rest)
			}
		}
	} else if k := h.Get("X-Api-Key"); k != "" {
		s.Carrier, v = "x-api-key", k
	} else {
		return s
	}
	s.Len = len(v)
	switch {
	case strings.HasPrefix(v, "sk-ant-oat"):
		s.Kind = "oauth_access"
	case strings.HasPrefix(v, "sk-ant-api"):
		s.Kind = "api_key"
	default:
		s.Kind = "other"
	}
	at := 0
	for i := 0; i < 3; i++ {
		j := strings.IndexByte(v[at:], '-')
		if j < 0 {
			return s
		}
		at += j + 1
	}
	s.PrefixLen = at
	return s
}

// String is one line fit for a log a person reads.
func (r Record) String() string {
	c := r.Credential
	return fmt.Sprintf("%s %s host=%s body=%d credential=%s/%s/%s prefix=%d len=%d headers=%s",
		r.Method, r.Path, r.Host, r.BodyBytes, c.Carrier, c.Scheme, c.Kind, c.PrefixLen, c.Len, strings.Join(r.Headers, ","))
}

// Upstream is the stand-in. The zero value answers every request with a
// short stream.
type Upstream struct {
	mu      sync.Mutex
	salt    []byte
	scripts map[string][]Step
	records []Record
	served  map[string]int
}

// Script sets how requests carrying credential are answered, in order; the
// last step repeats. The credential is the value after the scheme, or ""
// for requests that carry none or one with no script.
func (u *Upstream) Script(credential string, steps ...Step) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.scripts == nil {
		u.scripts = map[string][]Step{}
	}
	u.scripts[u.digest(credential)] = steps
}

// Records returns what arrived so far.
func (u *Upstream) Records() []Record {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Record(nil), u.records...)
}

// Served returns how many requests carried credential.
func (u *Upstream) Served(credential string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.served[u.digest(credential)]
}

// digest names a credential without keeping it. The empty credential keeps
// the empty name: it is the script for everything else. The caller holds
// u.mu.
func (u *Upstream) digest(credential string) string {
	if credential == "" {
		return ""
	}
	if u.salt == nil {
		u.salt = make([]byte, 32)
		if _, err := rand.Read(u.salt); err != nil {
			panic(err)
		}
	}
	mac := hmac.New(sha256.New, u.salt)
	mac.Write([]byte(credential))
	return hex.EncodeToString(mac.Sum(nil))
}

// credentialOf returns the credential a request carries: what follows a
// known scheme, else the whole value.
func credentialOf(h http.Header) string {
	if a := h.Get("Authorization"); a != "" {
		if scheme, rest, ok := strings.Cut(a, " "); ok {
			if l := strings.ToLower(scheme); l == "bearer" || l == "basic" {
				return strings.TrimSpace(rest)
			}
		}
		return a
	}
	return h.Get("X-Api-Key")
}

func (u *Upstream) next(r *http.Request, n int64) Step {
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	u.mu.Lock()
	defer u.mu.Unlock()
	cred := u.digest(credentialOf(r.Header))
	u.records = append(u.records, Record{Method: r.Method, Path: r.URL.EscapedPath(), Headers: names, Host: r.Host, Credential: ShapeOf(r.Header), BodyBytes: n})
	if u.served == nil {
		u.served = map[string]int{}
	}
	u.served[cred]++
	steps, ok := u.scripts[cred]
	if !ok {
		steps = u.scripts[""]
	}
	if len(steps) == 0 {
		return Step{Events: 3}
	}
	if len(steps) > 1 {
		if _, own := u.scripts[cred]; own {
			u.scripts[cred] = steps[1:]
		} else {
			u.scripts[""] = steps[1:]
		}
	}
	return steps[0]
}

// ServeHTTP answers one request by its script.
func (u *Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n, _ := io.Copy(io.Discard, r.Body)
	step := u.next(r, n)
	if step.Hang {
		<-r.Context().Done()
		return
	}
	for name, v := range step.Header {
		w.Header().Set(name, v)
	}
	if step.Status != 0 && step.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(step.Status)
		io.WriteString(w, step.Body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(w)
	flush.Flush()
	for i := 1; i <= step.Events; i++ {
		fmt.Fprintf(w, "event: message\ndata: {\"n\":%d}\n\n", i)
		flush.Flush()
		if step.CutAfter > 0 && i == step.CutAfter {
			panic(http.ErrAbortHandler)
		}
		if step.SilentAfter > 0 && i == step.SilentAfter {
			<-r.Context().Done()
			return
		}
		if step.Gap > 0 && i < step.Events {
			select {
			case <-time.After(step.Gap):
			case <-r.Context().Done():
				return
			}
		}
	}
}
