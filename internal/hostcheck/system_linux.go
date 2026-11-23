//go:build linux

package hostcheck

import (
	"bufio"
	"context"
	"net"
	"os"
	"strconv"
	"strings"
)

func systemReaders() Readers {
	// No keychain and no console session to lose: a background process
	// keeps what it had. The name service is read.
	return Readers{Resolve: resolve, LoadAverage: loadAverage, MemoryFreePercent: memoryFreePercent}
}

func loadAverage() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, os.ErrInvalid
	}
	return strconv.ParseFloat(f[0], 64)
}

func memoryFreePercent() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1
	}
	defer f.Close()
	var total, avail float64
	s := bufio.NewScanner(f)
	for s.Scan() {
		k, v, _ := strings.Cut(s.Text(), ":")
		n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 64)
		if err != nil {
			continue
		}
		switch k {
		case "MemTotal":
			total = n
		case "MemAvailable":
			avail = n
		}
	}
	if total <= 0 {
		return -1
	}
	return int(avail * 100 / total)
}

func resolve(ctx context.Context) error {
	_, err := net.DefaultResolver.LookupHost(ctx, ResolveName)
	return err
}
