//go:build !unix && !windows

package snapshot

import "os"

func lockAppend(*os.File) error { return nil }
func unlockAppend(*os.File)     {}
