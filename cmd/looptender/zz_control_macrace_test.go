//go:build darwin && race

package main

import "testing"

// Throwaway CI control: fails only in a race build on macOS.
func TestZZControlMacOSRaceOnly(t *testing.T) { t.Fatal("deliberate macOS race-only failure") }
