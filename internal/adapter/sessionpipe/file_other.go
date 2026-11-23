//go:build !unix && !windows

package sessionpipe

import "os"

func openEvents(path string) (*os.File, error)        { return os.Open(path) }
func fileIdentity(*os.File, os.FileInfo) string       { return "" }
func othersCanWrite(string, os.FileInfo) (bool, bool) { return false, false }
