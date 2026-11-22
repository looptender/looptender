//go:build !unix

package lock

import "errors"

// errInheritUnsupported: a lock cannot be handed to a new program image on
// this platform.
var errInheritUnsupported = errors.New("handing a lock across exec is not supported on this platform")

// KeepAcrossExec is not supported on this platform.
func (l *Lock) KeepAcrossExec() (uintptr, func(), error) { return 0, nil, errInheritUnsupported }

// Inherit is not supported on this platform.
func Inherit(uintptr, string) (*Lock, error) { return nil, errInheritUnsupported }
