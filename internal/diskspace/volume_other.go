//go:build !unix

package diskspace

import "path/filepath"

// VolumeID names the volume that holds dir by its drive or share; "" when it
// cannot be told.
func VolumeID(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if v := filepath.VolumeName(abs); v != "" {
		return "vol:" + v
	}
	return ""
}
