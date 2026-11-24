//go:build unix

// Package channel is the private channel between the account router's front
// and its router process (docs/design/router.md, "The parts"): a Unix socket
// in a directory only the user can enter. An answer on it can carry a login,
// so it is never a network port, every peer is checked to be the same user,
// and nothing that passes over it is logged.
//
// One request and one reply per connection, each a line of JSON.
package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/looptender/looptender/internal/router/front"
)

// SocketName is the socket's file in the private directory.
const SocketName = "router.sock"

// maxLine bounds one request or reply. An outcome carries a handful of
// header values; a decision one login.
const maxLine = 64 << 10

type request struct {
	Protocol int            `json:"protocol"`
	Op       string         `json:"op"` // "decide" or "report"
	Ask      *front.Ask     `json:"ask,omitempty"`
	Outcome  *front.Outcome `json:"outcome,omitempty"`
}

type reply struct {
	Decision *front.Decision `json:"decision,omitempty"`
	// Refused says why there is no decision. It never quotes a request.
	Refused string `json:"refused,omitempty"`
}

// Handler is the router as the channel sees it.
type Handler interface {
	Decide(context.Context, front.Ask) (front.Decision, error)
	Report(front.Outcome)
}

// ErrDirectory is returned for a directory that is not private to the user.
var ErrDirectory = errors.New("the router's directory must exist, belong to this user and be closed to everyone else")

// ErrRunning is returned when another router already answers in the
// directory.
var ErrRunning = errors.New("another router is already running in this directory")

// ownUID is this process's user. A test replaces it to stand for another
// user at either end.
var ownUID = os.Getuid

// protocol is the number this end speaks; a test changes it.
var protocol = front.Protocol

// beforeBind runs between checking the directory and binding in it; a test
// uses it to change the directory in that moment.
var beforeBind = func() {}

// CheckDirectory requires dir to be a real directory (not a link to one) of
// this user with no access for anyone else, and returns what it is, so a
// caller can tell later whether it is still the same one. Both ends apply
// it: the router before it listens, the front before every question.
//
// Only the directory itself is checked, not its parents. A parent others
// can write lets them put another directory in its place; that is caught
// not by walking the parents but by what follows the check on each side:
// the router confirms after binding that the directory is still the one it
// checked, and the front confirms that whoever answers is this user.
func CheckDirectory(dir string) (os.FileInfo, error) {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
		return nil, ErrDirectory
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != ownUID() {
		return nil, ErrDirectory
	}
	return st, nil
}

// samePeer reports whether the process at the other end of c runs as this
// user, by asking the system who it is.
func samePeer(c *net.UnixConn) bool {
	uid, ok := peerUID(c)
	return ok && uid == ownUID()
}

// maxPeers bounds the connections answered at once. A peer that connects
// and says nothing holds one for ten seconds; beyond the bound a new
// connection is closed unanswered, which the front takes as a miss.
const maxPeers = 64

// Server is the router's end.
type Server struct {
	ln     *net.UnixListener
	accept func() (*net.UnixConn, error) // ln.AcceptUnix, or a test's
	path   string
	wg     sync.WaitGroup
	peers  chan struct{}
}

// Listen opens the channel in dir. A socket file left by a router that is
// gone is removed, after confirming nobody answers on it.
func Listen(dir string) (*Server, error) {
	checked, err := CheckDirectory(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, SocketName)
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, ErrRunning
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	beforeBind()
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	// The directory bound in must still be the one that was checked, and
	// the socket in it closed to everyone else.
	now, err := CheckDirectory(dir)
	st, serr := os.Lstat(path)
	if err != nil || serr != nil || !os.SameFile(checked, now) || st.Mode().Perm()&0o077 != 0 {
		ln.Close()
		return nil, ErrDirectory
	}
	u := ln.(*net.UnixListener)
	return &Server{ln: u, accept: u.AcceptUnix, path: path, peers: make(chan struct{}, maxPeers)}, nil
}

// Serve answers requests with h until Close.
func (s *Server) Serve(h Handler) error {
	var wait time.Duration
	for {
		c, err := s.accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				return nil
			}
			// Out of file descriptors and the like pass: wait a little and
			// go on, as net/http does. The router does not stop for them.
			if temporary(err) {
				wait = backoff(wait)
				time.Sleep(wait)
				continue
			}
			s.wg.Wait()
			return err
		}
		wait = 0
		select {
		case s.peers <- struct{}{}:
		default:
			c.Close() // too many at once: unanswered
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.peers }()
			defer c.Close()
			s.answer(c, h)
		}()
	}
}

// Close stops the server and removes its socket.
func (s *Server) Close() error { return s.ln.Close() }

func (s *Server) answer(c *net.UnixConn, h Handler) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	// Only this user's own processes are answered, whatever the directory's
	// mode says.
	if !samePeer(c) {
		return
	}
	line, err := bufio.NewReaderSize(io.LimitReader(c, maxLine), maxLine).ReadBytes('\n')
	if err != nil {
		return
	}
	var rq request
	if json.Unmarshal(line, &rq) != nil {
		return
	}
	var out reply
	switch {
	case rq.Protocol != front.Protocol:
		// A front of another protocol is told so and treats the router as
		// absent: its requests go on their own login.
		out.Refused = "protocol"
	case rq.Op == "decide" && rq.Ask != nil:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		d, err := h.Decide(ctx, *rq.Ask)
		cancel()
		if err != nil {
			out.Refused = "unavailable"
		} else {
			out.Decision = &d
		}
	case rq.Op == "report" && rq.Outcome != nil:
		h.Report(*rq.Outcome)
	default:
		out.Refused = "request"
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	c.Write(append(b, '\n'))
}

// temporary reports whether an accept error is one that passes.
func temporary(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && (ne.Timeout() || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.ENOMEM))
}

// backoff doubles a wait from 5 ms up to a second.
func backoff(wait time.Duration) time.Duration {
	if wait == 0 {
		return 5 * time.Millisecond
	}
	if wait *= 2; wait > time.Second {
		wait = time.Second
	}
	return wait
}

// Client is the front's end. It is a front.Decider.
type Client struct {
	dir, path string
}

// NewClient returns a client for the channel in dir. It does not connect:
// a router that is not there yet is simply not there.
func NewClient(dir string) *Client {
	return &Client{dir: dir, path: filepath.Join(dir, SocketName)}
}

// absent reports whether a dial error means nobody is there to ask: no
// socket, or nobody behind it. Running out of descriptors, or a full
// backlog, is not that: the router is there and the front goes on asking.
func absent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOTDIR)
}

func (c *Client) exchange(ctx context.Context, rq request) (reply, error) {
	rq.Protocol = protocol
	// A question, and the login in its answer, go only to this user's own
	// router: the directory must be the user's and closed to everyone else
	// before the socket in it is trusted at all. Anything else is "no
	// router", never a decision.
	if _, err := CheckDirectory(c.dir); err != nil {
		return reply{}, front.ErrNoRouter
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		if absent(err) {
			return reply{}, front.ErrNoRouter
		}
		return reply{}, err
	}
	defer conn.Close()
	// And whoever answers must be this user, whatever the directory says.
	if u, ok := conn.(*net.UnixConn); !ok || !samePeer(u) {
		return reply{}, front.ErrNoRouter
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	b, err := json.Marshal(rq)
	if err != nil {
		return reply{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return reply{}, err
	}
	line, err := bufio.NewReaderSize(io.LimitReader(conn, maxLine), maxLine).ReadBytes('\n')
	if err != nil {
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		return reply{}, err
	}
	var out reply
	if err := json.Unmarshal(line, &out); err != nil {
		return reply{}, err
	}
	return out, nil
}

// Decide asks the router. A router that is not there, or speaks another
// protocol, is front.ErrNoRouter.
func (c *Client) Decide(ctx context.Context, ask front.Ask) (front.Decision, error) {
	out, err := c.exchange(ctx, request{Op: "decide", Ask: &ask})
	if err != nil {
		return front.Decision{}, err
	}
	if out.Refused == "protocol" {
		return front.Decision{}, front.ErrNoRouter
	}
	if out.Decision == nil {
		return front.Decision{}, errors.New("the router gave no decision")
	}
	return *out.Decision, nil
}

// Report tells the router how a request went. It gives up after a second
// and says nothing if nobody is there.
func (c *Client) Report(o front.Outcome) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c.exchange(ctx, request{Op: "report", Outcome: &o})
}
