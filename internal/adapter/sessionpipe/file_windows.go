//go:build windows

package sessionpipe

import (
	"fmt"
	"os"
	"syscall"
)

// openEvents opens the events file. A named pipe is not a regular file and is
// refused by the Stat before this open.
func openEvents(path string) (*os.File, error) { return os.Open(path) }

// fileIdentity names the file itself (volume serial number and file index),
// so a file replaced under the same name is read again from its start.
func fileIdentity(f *os.File, _ os.FileInfo) string {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return ""
	}
	return fmt.Sprintf("windows:%d:%d:%d", d.VolumeSerialNumber, d.FileIndexHigh, d.FileIndexLow)
}

// othersCanWrite is not known on Windows: mode bits do not say who can write
// a file, and reading its access list is left to doctor's DACL check.
func othersCanWrite(string, os.FileInfo) (refused bool, known bool) { return false, false }
