//go:build unix

package diskspace

import (
	"os"
	"strconv"
	"syscall"
)

// VolumeID names the volume that holds dir, so readings of loops on one
// volume can be told as one; "" when it cannot be read.
func VolumeID(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return "dev:" + strconv.FormatUint(uint64(st.Dev), 10)
}
