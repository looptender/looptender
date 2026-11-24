//go:build darwin

package hostcheck

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func systemReaders() Readers {
	return Readers{
		// Listing the user's keychains asks the security daemon of the
		// caller's session; outside a session it fails. It reads no secret
		// and never prompts.
		Keychain: func(ctx context.Context) error {
			return exec.CommandContext(ctx, "/usr/bin/security", "list-keychains", "-d", "user").Run()
		},
		Resolve:     resolve,
		Console:     consoleOwner,
		LoadAverage: loadAverage,
		Cores: func() int {
			n, err := unix.SysctlUint32("hw.ncpu")
			if err != nil {
				return 0
			}
			return int(n)
		},
		MemoryFreePercent: func() int {
			n, err := unix.SysctlUint32("kern.memorystatus_level")
			if err != nil {
				return -1
			}
			return int(n)
		},
	}
}

// consoleOwner: /dev/console belongs to the user logged in at the console,
// and to root when no one is.
func consoleOwner() (bool, bool) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return false, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, false
	}
	return st.Uid != 0, true
}

// loadAverage reads vm.loadavg: three fixed-point loads and their scale.
func loadAverage() (float64, error) {
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, err
	}
	if len(b) < 24 {
		return 0, syscall.EINVAL
	}
	scale := float64(binary.LittleEndian.Uint64(b[16:24]))
	if scale <= 0 {
		return 0, syscall.EINVAL
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / scale, nil
}

func resolve(ctx context.Context) error {
	_, err := net.DefaultResolver.LookupHost(ctx, ResolveName)
	return err
}
