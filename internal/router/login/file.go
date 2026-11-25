package login

import (
	"io"
	"os"
	"path/filepath"
)

const maxCredentialBytes = 64 << 10 // 64 KiB

// readPrivateFile reads a regular credential file owned by ownUID with no
// group/other permissions (mode & 0o077 == 0) inside a non-world/group-writable
// directory owned by ownUID. Symlinks, special files, and oversized files are rejected.
func readPrivateFile(path string, ownUID int) ([]byte, error) {
	return readCheckedFile(path, ownUID, true)
}

// readRegularFile reads a regular non-symlink file owned by ownUID inside a
// non-world/group-writable directory owned by ownUID (used for non-secret config).
func readRegularFile(path string, ownUID int) ([]byte, error) {
	return readCheckedFile(path, ownUID, false)
}

func readCheckedFile(path string, ownUID int, requirePrivate bool) ([]byte, error) {
	dirPath := filepath.Dir(path)
	dirInfo, err := os.Lstat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, ErrUnavailable
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUntrustedFile
	}
	if dirInfo.Mode().Perm()&0o022 != 0 || !sameOwner(dirInfo, ownUID) {
		return nil, ErrUntrustedFile
	}

	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, ErrUnavailable
	}
	if !st.Mode().IsRegular() {
		return nil, ErrUntrustedFile
	}
	if !sameOwner(st, ownUID) || (requirePrivate && st.Mode().Perm()&0o077 != 0) {
		return nil, ErrUntrustedFile
	}
	if st.Size() > maxCredentialBytes {
		return nil, ErrInvalid
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, ErrUnavailable
	}
	defer f.Close()

	// Re-verify the opened descriptor is still the same regular file.
	fst, err := f.Stat()
	if err != nil || !os.SameFile(st, fst) || !fst.Mode().IsRegular() || !sameOwner(fst, ownUID) || (requirePrivate && fst.Mode().Perm()&0o077 != 0) {
		return nil, ErrUntrustedFile
	}

	b, err := io.ReadAll(io.LimitReader(f, maxCredentialBytes+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(b) > maxCredentialBytes {
		return nil, ErrInvalid
	}
	return b, nil
}
