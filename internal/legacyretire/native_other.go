//go:build linux || (darwin && !cgo)

package legacyretire

import "context"

type Process struct {
	Token      [8]uint32
	Executable string
	Arguments  []string
}

func Inspect(int) (Process, error)                         { return Process{}, ErrUnavailable }
func ArmNative(context.Context, Binding) (Observer, error) { return nil, ErrUnavailable }

// Supported reports whether this build includes the Darwin native backend.
func Supported() bool { return false }
