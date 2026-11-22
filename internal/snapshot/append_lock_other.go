//go:build !unix && !windows

package snapshot

import "os"

func tryLockAppend(*os.File) (bool, error) { return true, nil }
func unlockAppend(*os.File)                {}
