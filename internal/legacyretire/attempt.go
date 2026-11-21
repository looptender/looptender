//go:build darwin || linux

// Package legacyretire reserves a single manual legacy-daemon retirement.
// It has no native signal backend or command entry point. Application/native
// evidence comes from an adapter whose locks remain held through completion.
package legacyretire

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var ErrUnavailable = errors.New("retirement attempt unavailable; preserve evidence")
var ErrSpent = errors.New("retirement attempt consumed; no replay or automatic replacement")

// Identity binds a reviewed file, not just its pathname.
type Identity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

// Binding is explicit application evidence, not authority derived from a PID.
// SourceDigest binds current reviewed sources, not old in-memory configuration.
type Binding struct {
	ConfigPath       string    `json:"configPath"`
	StatePath        string    `json:"statePath"`
	SourceDigest     string    `json:"sourceDigest"`
	ControlDigest    string    `json:"controlDigest"`
	ControlIdentity  Identity  `json:"controlIdentity"`
	Owner            string    `json:"owner"`
	Generation       uint64    `json:"generation"`
	PID              int       `json:"pid"`
	Build            string    `json:"build"`
	StartedAt        string    `json:"startedAt"`
	StateID          string    `json:"stateID"`
	Executable       string    `json:"executable"`
	ExecutableDigest string    `json:"executableDigest"`
	Arguments        []string  `json:"arguments"`
	Token            [8]uint32 `json:"token"`
}

// cloneBinding prevents adapter-owned mutable buffers from changing the
// immutable prepared expectation. Strings and native token arrays are values.
func cloneBinding(b Binding) Binding {
	b.Arguments = slices.Clone(b.Arguments)
	return b
}

type intent struct {
	Schema  int     `json:"schema"`
	Nonce   string  `json:"nonce"`
	Binding Binding `json:"binding"`
}

// Ticket is returned only after preparation is durable. Callers must retain this
// exact reviewed ticket outside the attempt directory. Missing tickets/records
// cannot be reconstructed from an endpoint, historical PID or supplied binding.
type Ticket struct {
	Root                           string
	Directory, Intent, Lock, Ready Identity
	Digest                         string
}

// Guard is acquired in tick->control order by the adapter. Verify returns fresh
// authenticated application and native facts from the same held snapshots.
// Arm must precede dispatch and bind its observer to that verified incarnation.
type Guard interface {
	Verify(context.Context) (Binding, error)
	Arm(context.Context, Binding) (Observer, error)
	Retired(context.Context, Binding) (Evidence, error)
	Close() error
}
type Observer interface {
	// Terminate requests one token-directed SIGTERM, never a raw PID fallback.
	Terminate(context.Context) error
	// WaitExit returns true only for the armed incarnation's native death.
	// Exec, observer loss, timeout or ambiguity returns false/error.
	WaitExit(context.Context) (bool, error)
	Close() error
}
type Evidence struct {
	EndpointAbsent  bool
	DaemonLockFree  bool
	ControlDigest   string
	ControlIdentity Identity
}
type Acquire func(context.Context, Binding) (Guard, error)

// hooks are per-call filesystem fault seams used only by package tests.
type hooks struct{ before func(string) error }

func (h hooks) check(stage string) error {
	if h.before != nil {
		return h.before(stage)
	}
	return nil
}
func identity(st *unix.Stat_t) Identity { return Identity{uint64(st.Dev), st.Ino} }
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func (b Binding) valid() bool {
	_, startedErr := time.Parse(time.RFC3339Nano, b.StartedAt)
	return startedErr == nil && filepath.IsAbs(b.ConfigPath) && filepath.IsAbs(b.StatePath) && filepath.IsAbs(b.Executable) &&
		validHash(b.SourceDigest) && validHash(b.ControlDigest) && validHash(b.StateID) && validHash(b.ExecutableDigest) &&
		b.ControlIdentity.Inode != 0 && b.Owner != "" && b.Generation > 0 && b.PID > 1 && b.Build != "" && b.StartedAt != "" &&
		len(b.Arguments) > 0 && b.Token[5] == uint32(b.PID) && b.Token[7] != 0
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func private(st *unix.Stat_t, directory bool) bool {
	kind := uint32(unix.S_IFREG)
	if directory {
		kind = unix.S_IFDIR
	}
	return uint32(st.Mode)&unix.S_IFMT == kind && st.Mode&0077 == 0 && st.Uid == uint32(os.Geteuid()) && (directory || st.Nlink == 1)
}
func statFD(fd int, directory bool) (Identity, error) {
	var st unix.Stat_t
	if e := unix.Fstat(fd, &st); e != nil || !private(&st, directory) {
		return Identity{}, ErrUnavailable
	}
	return identity(&st), nil
}
func openRoot(path string) (int, Identity, error) {
	abs, e := filepath.Abs(path)
	if e != nil || abs != path {
		return -1, Identity{}, ErrUnavailable
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return -1, Identity{}, ErrUnavailable
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, Identity{}, ErrUnavailable
	}
	id, e := statFD(fd, true)
	if e != nil {
		unix.Close(fd)
		return -1, Identity{}, e
	}
	return fd, id, nil
}
func openRecord(dir int, name string, flags int) (int, Identity, error) {
	fd, e := unix.Openat(dir, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return -1, Identity{}, ErrUnavailable
	}
	id, e := statFD(fd, false)
	if e != nil {
		unix.Close(fd)
		return -1, Identity{}, e
	}
	return fd, id, nil
}
func sameName(dir int, name string, id Identity) bool {
	var st unix.Stat_t
	return unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && private(&st, false) && identity(&st) == id
}
func create(dir int, name string, data []byte, h hooks) (Identity, error) {
	if e := h.check(name + "-create"); e != nil {
		return Identity{}, e
	}
	fd, e := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return Identity{}, e
	}
	f := os.NewFile(uintptr(fd), name)
	if e = h.check(name + "-write"); e == nil {
		var n int
		n, e = f.Write(data)
		if e == nil && n != len(data) {
			e = io.ErrShortWrite
		}
	}
	if e == nil {
		e = h.check(name + "-sync")
	}
	if e == nil {
		e = f.Sync()
	}
	id, se := statFD(fd, false)
	ce := f.Close()
	if e != nil {
		return Identity{}, e
	}
	if se != nil {
		return Identity{}, se
	}
	return id, ce
}
func directorySync(fd int, stage string, h hooks) error {
	if e := h.check(stage); e != nil {
		return e
	}
	return unix.Fsync(fd)
}

// Prepare creates one new private directory. On any failure it leaves evidence
// in place and returns no ticket; it never repairs or reuses an existing root.
func Prepare(path string, b Binding) (Ticket, error) { return prepare(path, b, hooks{}) }
func prepare(path string, b Binding, h hooks) (Ticket, error) {
	var t Ticket
	if !b.valid() || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return t, ErrUnavailable
	}
	canonicalParent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil || canonicalParent != filepath.Dir(path) {
		return t, ErrUnavailable
	}
	if e := os.Mkdir(path, 0700); e != nil {
		return t, e
	}
	fd, id, e := openRoot(path)
	if e != nil {
		return t, e
	}
	defer unix.Close(fd)
	nonce := make([]byte, 32)
	if _, e = rand.Read(nonce); e != nil {
		return t, e
	}
	raw, e := json.Marshal(intent{1, hex.EncodeToString(nonce), b})
	if e != nil || len(raw) > 32768 {
		return t, ErrUnavailable
	}
	t = Ticket{Root: path, Directory: id, Digest: digest(raw)}
	if t.Intent, e = create(fd, "intent", raw, h); e != nil {
		return Ticket{}, e
	}
	if t.Lock, e = create(fd, "reservation", []byte(t.Digest), h); e != nil {
		return Ticket{}, e
	}
	if t.Ready, e = create(fd, "ready", []byte(t.Digest), h); e != nil {
		return Ticket{}, e
	}
	if e = directorySync(fd, "prepare-directory-sync", h); e != nil {
		return Ticket{}, e
	}
	parent, e := os.Open(filepath.Dir(path))
	if e != nil {
		return Ticket{}, e
	}
	e = parent.Sync()
	ce := parent.Close()
	if e != nil {
		return Ticket{}, e
	}
	if ce != nil {
		return Ticket{}, ce
	}
	return t, nil
}
func read(dir int, name string, id Identity) ([]byte, error) {
	fd, got, e := openRecord(dir, name, unix.O_RDONLY)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if got != id {
		return nil, ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(f, 32769))
	if e != nil || len(b) > 32768 || !sameName(dir, name, id) {
		return nil, ErrUnavailable
	}
	return b, nil
}
func absent(dir int, name string) bool {
	var st unix.Stat_t
	return unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW) == unix.ENOENT
}
func load(dir int, t Ticket) (Binding, error) {
	raw, e := read(dir, "intent", t.Intent)
	if e != nil || digest(raw) != t.Digest {
		return Binding{}, ErrUnavailable
	}
	var in intent
	if json.Unmarshal(raw, &in) != nil || in.Schema != 1 || !validHash(in.Nonce) || !in.Binding.valid() {
		return Binding{}, ErrUnavailable
	}
	// Exact canonical bytes reject unknown/duplicate/case-fold keys, null required
	// scalars and schema aliases; the digest is from preparation, not input text.
	canonical, e := json.Marshal(in)
	if e != nil || !bytes.Equal(raw, canonical) {
		return Binding{}, ErrUnavailable
	}
	return in.Binding, nil
}

// Retire never launches a replacement or edits control. Any spent marker makes
// re-entry unavailable, including a crash before dispatch or a lost reply.
func Retire(ctx context.Context, t Ticket, acquire Acquire) error {
	return retire(ctx, t, acquire, hooks{})
}
func retire(ctx context.Context, t Ticket, acquire Acquire, h hooks) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("bounded retirement context required")
	}
	dir, id, e := openRoot(t.Root)
	if e != nil {
		return e
	}
	defer unix.Close(dir)
	if id != t.Directory {
		return ErrUnavailable
	}
	l, lid, e := openRecord(dir, "reservation", unix.O_RDWR)
	if e != nil {
		return e
	}
	defer unix.Close(l)
	if lid != t.Lock || unix.Flock(l, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrUnavailable
	}
	defer unix.Flock(l, unix.LOCK_UN)
	recheck := func() error {
		root, rid, e := openRoot(t.Root)
		if e != nil {
			return e
		}
		unix.Close(root)
		if rid != t.Directory || !sameName(dir, "reservation", t.Lock) || !sameName(dir, "intent", t.Intent) {
			return ErrUnavailable
		}
		raw, e := read(dir, "intent", t.Intent)
		if e != nil || digest(raw) != t.Digest {
			return ErrUnavailable
		}
		raw, e = read(dir, "reservation", t.Lock)
		if e != nil || string(raw) != t.Digest {
			return ErrUnavailable
		}
		return nil
	}
	if e = recheck(); e != nil {
		return e
	}
	if !absent(dir, "spent") {
		return ErrSpent
	}
	b, e := load(dir, t)
	if e != nil {
		return e
	}
	ready, e := read(dir, "ready", t.Ready)
	if e != nil || string(ready) != t.Digest {
		return ErrUnavailable
	}
	lockBytes, e := read(dir, "reservation", t.Lock)
	if e != nil || string(lockBytes) != t.Digest {
		return ErrUnavailable
	}
	g, e := acquire(ctx, cloneBinding(b))
	if e != nil {
		return e
	}
	defer g.Close()
	verify := func() error {
		fresh, e := g.Verify(ctx)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(fresh, b) {
			return ErrUnavailable
		}
		return recheck()
	}
	if e = verify(); e != nil {
		return e
	}
	observer, e := g.Arm(ctx, cloneBinding(b))
	if e != nil {
		return e
	}
	defer observer.Close()
	if e = verify(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	// Atomic exclusive link publishes immutable consumed evidence without a
	// missing/default intermediate or overwriting anything. Both names may exist
	// after a crash; spent always wins. Never unlink spent or reservation.
	if e = h.check("consume-link"); e != nil {
		return e
	}
	if e = unix.Linkat(dir, "ready", dir, "spent", 0); e != nil {
		return e
	}
	if e = h.check("consume-unlink"); e == nil {
		e = unix.Unlinkat(dir, "ready", 0)
	}
	if e != nil {
		return e
	}
	if e = directorySync(dir, "consume-directory-sync", h); e != nil {
		return e
	}
	// Every subsequent refusal spends the attempt, including final binding drift.
	if e = verify(); e != nil {
		return e
	}
	spent, spentErr := read(dir, "spent", t.Ready)
	if spentErr != nil || string(spent) != t.Digest || !absent(dir, "ready") {
		return ErrUnavailable
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = observer.Terminate(ctx); e != nil {
		return e
	}
	dead, e := observer.WaitExit(ctx)
	if e != nil || !dead {
		return ErrSpent
	}
	evidence, e := g.Retired(ctx, cloneBinding(b))
	if e != nil {
		return e
	}
	if !evidence.EndpointAbsent || !evidence.DaemonLockFree || evidence.ControlDigest != b.ControlDigest || evidence.ControlIdentity != b.ControlIdentity {
		return ErrSpent
	}
	if e = recheck(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if _, e = create(dir, "completed", []byte(t.Digest), h); e != nil {
		return e
	}
	if e = directorySync(dir, "completed-directory-sync", h); e != nil {
		return e
	}
	return h.check("completion-ack")
}
