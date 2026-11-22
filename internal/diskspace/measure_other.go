//go:build !unix && !windows

package diskspace

import "errors"

func measure(string) (Reading, error) { return Reading{}, errors.New("not available on this platform") }
