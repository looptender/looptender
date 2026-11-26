//go:build !darwin && !linux

package main

import (
	"errors"
	"os"
)

// Fail closed until a native no-follow/nonblocking descriptor reader is tested.
func openUsageEvidence(path string) (*os.File, error) {
	return nil, errors.New("usage evidence reader unsupported on this platform")
}
