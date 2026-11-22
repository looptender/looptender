//go:build windows

package diskspace

import "golang.org/x/sys/windows"

func measure(dir string) (Reading, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return Reading{}, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return Reading{}, err
	}
	return Reading{Available: avail, Total: total}, nil
}
