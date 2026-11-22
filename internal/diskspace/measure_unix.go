//go:build unix

package diskspace

import "golang.org/x/sys/unix"

func measure(dir string) (Reading, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(dir, &s); err != nil {
		return Reading{}, err
	}
	bsize := uint64(s.Bsize)
	return Reading{Available: uint64(s.Bavail) * bsize, Total: uint64(s.Blocks) * bsize}, nil
}
