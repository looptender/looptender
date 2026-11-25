//go:build !darwin && !linux

package main

import "os"

// isTerminal is false where connecting an account is not supported.
func isTerminal(*os.File) bool { return false }
