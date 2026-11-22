//go:build !darwin && !linux

package main

import (
	"github.com/looptender/looptender/internal/snapshot"
	"os"
)

// No canonical publication until native replacement/marker and directory
// durability semantics have been established for this platform.
func freezeSupported() bool                          { return false }
func openFreezeRecord(path string) (*os.File, error) { return snapshot.OpenRecord(path) }
func syncFreezeDirectory(path string) error          { return errFreezeUnavailable }

func freezeNativeID(info os.FileInfo) string { return "" }
