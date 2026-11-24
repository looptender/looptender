//go:build unix && !linux && !darwin

package channel

import "net"

// systemPeerUID cannot ask who the peer is on this system: nobody is
// answered and nobody is trusted, and every request goes on its own login.
func systemPeerUID(*net.UnixConn) (int, bool) { return 0, false }
