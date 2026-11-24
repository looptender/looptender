//go:build unix && !linux && !darwin

package channel

import "net"

// samePeer is false where this code has no way to ask who the peer is: the
// channel then answers nobody, and every request goes on its own login.
func samePeer(*net.UnixConn) bool { return false }
