package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Writes to a loop's records go through Write, so that a full disk, a
// failed write or a short one is always an error and never a record cut
// short: every record is written to a temporary file that is renamed over
// the old one only after the whole write, and the one append-only log,
// runs.jsonl, is cut back to its old length (AppendLine).

// ErrShortWrite is a write that stored fewer bytes than asked without an
// error of its own.
var ErrShortWrite = errors.New("short write")

var (
	faultMu sync.Mutex
	fault   func(path string) (keep int, err error)
)

// InjectWriteFaults makes Write fail, for the rest of a test, when f returns
// an error for the file's path: it first stores keep bytes (a short write),
// then returns the error. f nil removes it. It panics outside a test binary,
// so no command, flag or environment variable can make writes fail.
func InjectWriteFaults(f func(path string) (keep int, err error)) {
	if !testing.Testing() {
		panic("snapshot: InjectWriteFaults called outside a test binary")
	}
	faultMu.Lock()
	fault = f
	faultMu.Unlock()
}

// ENOSPC is the error a full disk gives, for tests and for callers that ask
// whether a failure was a full disk.
var ENOSPC error = syscall.ENOSPC

// Write writes all of data to f, or returns an error: a short write is an
// error. In a test it can be made to fail (InjectWriteFaults).
func Write(f *os.File, data []byte) error {
	faultMu.Lock()
	inject := fault
	faultMu.Unlock()
	if inject != nil {
		if keep, err := inject(f.Name()); err != nil {
			if keep > len(data) {
				keep = len(data)
			}
			if keep > 0 {
				_, _ = f.Write(data[:keep])
			}
			return err
		}
	}
	n, err := f.Write(data)
	if err == nil && n < len(data) {
		err = ErrShortWrite
	}
	return err
}

// WriteFileAtomic replaces path with data: a temporary file beside it is
// written in full, flushed when sync is set, and renamed over path
// (ReplaceAtomic: no availability marker). On any failure path is left as it
// was and the temporary file is removed.
func WriteFileAtomic(path string, data []byte, perm os.FileMode, sync bool) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := Write(tmp, data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if sync {
		if err := Sync(tmp); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return ReplaceAtomic(name, path)
}

// AppendLine appends one line to an append-only log, whole or not at all: a
// failed or short write is cut back to the log's length before it, so no
// later line is joined to a torn one. A log left torn by an earlier crash
// gets a line break first. Every appender holds an exclusive lock on the
// log, only across the length read, the write and the cut, so the cut can
// only ever remove this call's own bytes, never another process's line.
//
// The lock is waited for at most AppendLockWait: a holder that is suspended,
// or stuck on a hung file system, must not stall every appender, a check
// among them. Without the lock the line is still appended, whole if the
// write succeeds, but a failed write is not cut back (it could cut another
// appender's line); the next line then starts on a line of its own.
func AppendLine(path string, line []byte, perm os.FileMode) error {
	return appendLine(path, func(bool) []byte { return line }, perm, false)
}

// AppendLineSync is AppendLine, flushing the line to the device before it
// returns.
func AppendLineSync(path string, line []byte, perm os.FileMode) error {
	return appendLine(path, func(bool) []byte { return line }, perm, true)
}

// AppendLineNoting is AppendLine for a log whose line can say how it was
// written: line is called with whether the lock was obtained.
func AppendLineNoting(path string, line func(locked bool) []byte, perm os.FileMode) error {
	return appendLine(path, line, perm, false)
}

// AppendLockWait is how long an appender waits for the log's lock.
var AppendLockWait = 3 * time.Second

// AppendLockSkip is how long, after a wait for a log's lock ran out, this
// process tries the lock only once instead of waiting again: a stuck holder
// costs one wait per check, not one per event.
var AppendLockSkip = time.Minute

var (
	unobtainableMu sync.Mutex
	unobtainable   = map[string]time.Time{} // log path → until when not to wait
)

func appendLine(path string, build func(locked bool) []byte, perm os.FileMode, sync bool) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	// The lock is taken on a handle of its own: on Windows a handle opened
	// for appending lacks the access a lock needs.
	l, err := os.Open(path)
	if err != nil {
		return err
	}
	defer l.Close()
	wait := AppendLockWait
	unobtainableMu.Lock()
	if until, ok := unobtainable[path]; ok && time.Now().Before(until) {
		wait = 0 // it was unobtainable a moment ago: one try only
	}
	unobtainableMu.Unlock()
	locked := false
	for deadline := time.Now().Add(wait); ; time.Sleep(20 * time.Millisecond) {
		ok, err := tryLockAppend(l)
		if err != nil {
			return err
		}
		if ok {
			locked = true
			break
		}
		if !time.Now().Before(deadline) {
			break // held elsewhere for too long: append without it
		}
	}
	unobtainableMu.Lock()
	if locked {
		delete(unobtainable, path)
	} else if wait > 0 {
		unobtainable[path] = time.Now().Add(AppendLockSkip)
	}
	unobtainableMu.Unlock()
	if locked {
		defer unlockAppend(l)
	}
	line := build(locked)
	info, err := f.Stat()
	if err != nil {
		return err
	}
	end := info.Size()
	if end > 0 {
		if r, err := os.Open(path); err == nil {
			last := make([]byte, 1)
			if _, err := r.ReadAt(last, end-1); err == nil && last[0] != '\n' {
				line = append([]byte{'\n'}, line...)
			}
			r.Close()
		}
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line = append(line, '\n')
	}
	// Without the lock another appender can end its line between the length
	// read above and this write, so a line break added for a torn line may
	// leave an empty line instead. That is accepted: nothing is lost or
	// joined, the routes history reader skips empty lines, and runs.jsonl
	// has no reader in the product.
	if err := Write(f, line); err != nil {
		if now, serr := f.Stat(); locked && serr == nil && now.Size() > end {
			// Only this call's bytes are past end: the lock keeps every
			// other appender out. Cut by a handle of its own: on Windows one
			// opened for appending cannot change a file's length.
			if w, oerr := os.OpenFile(path, os.O_WRONLY, 0); oerr == nil {
				_ = w.Truncate(end)
				w.Close()
			}
		}
		return err
	}
	if sync {
		return Sync(f)
	}
	return nil
}

// HoldAppendLockForTest takes the appenders' lock on the log at path, as
// another process holding it would, until release is called. It panics
// outside a test binary.
func HoldAppendLockForTest(path string) (release func(), err error) {
	if !testing.Testing() {
		panic("snapshot: HoldAppendLockForTest called outside a test binary")
	}
	l, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	ok, err := tryLockAppend(l)
	if err != nil || !ok {
		l.Close()
		if err == nil {
			err = errors.New("lock held")
		}
		return nil, err
	}
	return func() { unlockAppend(l); l.Close() }, nil
}
