package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/registry"
)

// A running page retains its reader, not its original registry membership.
// Exercise the encoded refresh path and the native menu's projection together.
func TestDockDiscoveryRefreshPreservesExplicitRegistryAuthority(t *testing.T) {
	d, root := dockFixtureReader(t)
	d.accountsDir = filepath.Join(root, "accounts")
	f, err := registry.Load(d.directory)
	if err != nil {
		t.Fatal(err)
	}
	entry := f.Entries[0]
	if err := registry.Remove(d.directory, entry.ID); err != nil {
		t.Fatal(err)
	}

	read := func(expire bool) dockReport {
		t.Helper()
		if expire {
			d.mu.Lock()
			for k, c := range d.cache {
				c.at = time.Now().Add(-3 * time.Second)
				d.cache[k] = c
			}
			d.mu.Unlock()
		}
		before := treeDigest(t, root)
		body, err := d.json(false)
		if err != nil {
			t.Fatal(err)
		}
		if after := treeDigest(t, root); after != before {
			t.Fatal("refresh changed fictional loop or registry files")
		}
		var r dockReport
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		menu := buildPanelList(r)
		if len(menu.Loops) != len(r.Loops) || menu.Unread != r.Unread {
			t.Fatal("native menu projection lost registry coverage")
		}
		for i, loop := range r.Loops {
			if menu.Loops[i].Key != loop.Key || menu.Loops[i].Name != loop.Name || menu.Loops[i].State != loop.State {
				t.Fatal("native menu projection changed loop identity or state")
			}
		}
		return r
	}
	contains := func(r dockReport) bool {
		for _, loop := range r.Loops {
			if loop.Name == entry.Loop {
				return true
			}
		}
		return false
	}
	// The config still exists, but reads must not discover it without registration.
	if r := read(false); contains(r) || len(r.Loops) != len(f.Entries)-1 {
		t.Fatal("unregistered config was discovered")
	}
	added, err := registry.Add(d.directory, entry.Loop, entry.ConfigPath, entry.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if contains(read(false)) {
		t.Fatal("fixture did not exercise the existing short snapshot cache")
	}
	if r := read(true); !contains(r) || len(r.Loops) != len(f.Entries) {
		t.Fatal("new registration missing after cache expiry")
	}

	if err := os.Rename(entry.ConfigPath, entry.ConfigPath+".unavailable"); err != nil {
		t.Fatal(err)
	}
	r := read(true)
	if !contains(r) || r.Unread != 1 {
		t.Fatal("stale reference must remain visible as unreadable")
	}
	if err := registry.Remove(d.directory, added.ID); err != nil {
		t.Fatal(err)
	}
	if r := read(true); contains(r) || r.Unread != 0 {
		t.Fatal("explicitly removed reference survived refresh")
	}
}
