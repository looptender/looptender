package pointer

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrFile means the settings file, or the directory it is in, is not one a
// pointer may be written to: a link, something that is not a regular file,
// or a directory that someone else can write.
var ErrFile = errors.New("the settings file or its directory cannot be trusted with a pointer")

// read returns the file's bytes and mode. A file that does not exist is
// empty bytes and exists false. The file may not be a link, and its
// directory may not be writable by anyone else: the pointer carries the
// client secret.
func read(path string) (b []byte, mode os.FileMode, exists bool, err error) {
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o022 != 0 {
		return nil, 0, false, ErrFile
	}
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, 0o600, false, nil
	}
	if err != nil || !st.Mode().IsRegular() {
		return nil, 0, false, ErrFile
	}
	b, err = os.ReadFile(path)
	if err != nil {
		return nil, 0, false, err
	}
	return b, st.Mode().Perm(), true, nil
}

// write replaces the file's content in one step: a new file beside it,
// closed to everyone else, then a rename over it. A reader sees the old
// file or the new one, never part of one; when the write cannot be
// completed (a full disk), the old file stays.
func write(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".looptender-pointer-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, mode)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// PointFile writes p into the settings file at path, making the file when
// there is none. The file is closed to everyone but the user from then on,
// because it now carries the client secret. The record that comes back is
// what UnpointFile and RemoveFile need.
func PointFile(path string, p Pointer) (Record, error) {
	b, _, _, err := read(path)
	if err != nil {
		return Record{}, err
	}
	out, rec, err := Point(b, p)
	if err != nil {
		return Record{}, err
	}
	if err := write(path, out, 0o600); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// UnpointFile rewrites the pointer in the settings file at path and makes
// the client's secret worthless in the same step. The secret is rotated
// first: if the write then fails, sessions still send a secret that no
// longer names a client, and the front forwards such requests unchanged,
// which is the safe direction.
func UnpointFile(path string, rec Record, clients *Clients, client string) (Record, error) {
	b, mode, exists, err := read(path)
	if err != nil {
		return rec, err
	}
	if !exists {
		return rec, ErrChanged
	}
	out, next, err := Unpoint(b, rec)
	if err != nil {
		return rec, err
	}
	if err := clients.Rotate(client); err != nil {
		return rec, err
	}
	if err := write(path, out, mode); err != nil {
		return rec, err
	}
	return next, nil
}

// RemoveFile takes the pointer out of the settings file at path. When the
// file is exactly what was last written, it gets back its original bytes
// and its original mode, or is removed when the pointer made it. originalMode
// is the mode the file had before PointFile.
func RemoveFile(path string, rec Record, originalMode os.FileMode) error {
	b, mode, exists, err := read(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	restored := digest(b) == rec.Written
	out, absent, err := Remove(b, rec)
	if err != nil {
		return err
	}
	if absent {
		return os.Remove(path)
	}
	if restored && originalMode != 0 {
		mode = originalMode
	}
	return write(path, out, mode)
}

// Mode returns the settings file's mode, 0 when there is no file, so that a
// caller can keep it for RemoveFile before pointing.
func Mode(path string) os.FileMode {
	st, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	return st.Mode().Perm()
}
