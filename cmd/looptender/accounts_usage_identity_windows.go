package main

import (
	"fmt"
	"os"

	"github.com/looptender/looptender/internal/accounts"
	"golang.org/x/sys/windows"
)

func accountUsageIdentity(path string, info os.FileInfo) (string, error) {
	if info == nil {
		return "", accounts.ErrChanged
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", accounts.ErrChanged
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", accounts.ErrChanged
	}
	f := os.NewFile(uintptr(handle), path)
	if f == nil {
		windows.CloseHandle(handle)
		return "", accounts.ErrChanged
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !os.SameFile(info, current) {
		return "", accounts.ErrChanged
	}
	var id windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &id) != nil || id.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", accounts.ErrChanged
	}
	return fmt.Sprintf("%d:%d:%d", id.VolumeSerialNumber, id.FileIndexHigh, id.FileIndexLow), nil
}
