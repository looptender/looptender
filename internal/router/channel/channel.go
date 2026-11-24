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

// checkDirectory requires dir to be a real directory of this user with no
// access for anyone else.
func checkDirectory(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
		return ErrDirectory
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != os.Getuid() {
		return ErrDirectory
	}
	return nil
}

// Server is the router's end.
type Server struct {
	ln   *net.UnixListener
	path string
	wg   sync.WaitGroup
}

// Listen opens the channel in dir. A socket file left by a router that is
// gone is removed, after confirming nobody answers on it.
func Listen(dir string) (*Server, error) {
	if err := checkDirectory(dir); err != nil {
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
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err != nil || st.Mode().Perm()&0o077 != 0 {
		ln.Close()
		return nil, ErrDirectory
	}
	return &Server{ln: ln.(*net.UnixListener), path: path}, nil
}

// Serve answers requests with h until Close.
func (s *Server) Serve(h Handler) error {
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			s.wg.Wait()
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
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

// Client is the front's end. It is a front.Decider.
type Client struct {
	path string
}

// NewClient returns a client for the channel in dir. It does not connect:
// a router that is not there yet is simply not there.
func NewClient(dir string) *Client { return &Client{path: filepath.Join(dir, SocketName)} }

func (c *Client) exchange(ctx context.Context, rq request) (reply, error) {
	rq.Protocol = front.Protocol
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		// No socket, or nobody behind it: there is no router to ask.
		return reply{}, front.ErrNoRouter
	}
	defer conn.Close()
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
