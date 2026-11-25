//go:build !unix

package telegram

import "os"

// privateFile: unix permission bits and owners are not what protects a
// file on this platform, so they are not judged here.
func privateFile(os.FileInfo) bool { return true }
