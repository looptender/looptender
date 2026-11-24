//go:build !unix

// Package channel is the private channel between the account router's front
// and its router process. It exists only on Unix systems so far.
package channel
