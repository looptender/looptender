package lock

import (
	"errors"
	"os"
	"path/filepath"
)

// Inspection is a point-in-time diagnostic, never authority to take a lock or
// control its holder. Holder identities are unavailable: lock files contain no
// authenticated conversation or run binding. No file content is returned.
type Inspection struct {
	Name         string `json:"name"`
	Availability string `json:"availability"`
	Reason       string `json:"reason"`
	Metadata     string `json:"metadata"`
}

// Inspect examines an existing tick or daemon lock without acquiring any lock,
// creating files, or changing metadata. Unsupported queries remain unavailable.
func Inspect(dir, name string) Inspection {
	out := Inspection{Name: name, Availability: "unavailable", Reason: "lock_unreadable", Metadata: "unreadable"}
	if name != Tick && name != Daemon {
		out.Name, out.Reason = "", "unsupported_lock"
		return out
	}
	path := filepath.Join(dir, name+".lock")
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		out.Availability, out.Reason, out.Metadata = "none", "lock_file_absent", "not_recorded"
		return out
	}
	if err != nil {
		return out
	}
	if !before.Mode().IsRegular() {
		out.Reason = "lock_not_regular"
		return out
	}
	f, err := openInspection(path)
	if err != nil {
		return out
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		out.Reason = "lock_changed"
		return out
	}
	out.Metadata = "not_recorded"
	// Current writers store no holder metadata. Never interpret arbitrary bytes
	// as a PID, path, conversation, or proof of ownership.
	if opened.Size() != 0 {
		out.Metadata = "unavailable"
	}
	out.Availability, out.Reason = inspectFile(f)
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		out.Availability, out.Reason = "unavailable", "lock_changed"
	}
	if out.Availability == "none" && opened.Size() != 0 {
		out.Metadata = "stale"
	}
	return out
}
