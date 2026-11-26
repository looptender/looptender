//go:build !unix

package lock

import "os"

func openInspection(path string) (*os.File, error) { return os.Open(path) }
