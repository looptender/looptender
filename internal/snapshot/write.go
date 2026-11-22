package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
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
// written in full, flushed when sync is set, and renamed over path. On any
// failure path is left as it was and the temporary file is removed.
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
	return Replace(name, path)
}

// AppendLine appends one line to an append-only log, whole or not at all: a
// failed or short write is cut back to the log's length before it, so no
// later line is joined to a torn one. A log left torn by an earlier crash
// gets a line break first. The write is an O_APPEND write, so lines from
// other processes are not overwritten; the cut is made only while the log
// holds no more than this line's bytes past where it ended.
func AppendLine(path string, line []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer f.Close()
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
	if err := Write(f, line); err != nil {
		if now, serr := f.Stat(); serr == nil && now.Size() > end && now.Size()-end <= int64(len(line)) {
			_ = f.Truncate(end) // nothing of this line stays
		}
		return err
	}
	return nil
}
