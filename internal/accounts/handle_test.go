package accounts

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// A handle has the shape of a catalog ID, differs from it, is stable for one
// catalog and differs across catalogs, and only its own catalog resolves it.
func TestHandleNamesAnAccountWithoutItsID(t *testing.T) {
	id := strings.Repeat("a", 32)
	f := File{Salt: strings.Repeat("1", 64), Entries: []Entry{{ID: id, Label: "Example Codex", Provider: "codex"}}}
	h := f.Handle(id)
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(h) || h == id || h != f.Handle(id) {
		t.Fatalf("handle %q", h)
	}
	if got, ok := f.ByHandle(h); !ok || got != id {
		t.Fatalf("ByHandle %q %v", got, ok)
	}
	other := File{Salt: strings.Repeat("2", 64), Entries: f.Entries}
	if other.Handle(id) == h {
		t.Fatal("two catalogs give one account the same handle")
	}
	if _, ok := other.ByHandle(h); ok {
		t.Fatal("another catalog resolved the handle")
	}
	if _, ok := f.ByHandle(id); ok {
		t.Fatal("a catalog ID resolved as a handle")
	}
	if c := Project(f, time.Now()).Cards[0]; c.Handle != h {
		t.Fatalf("card handle %q", c.Handle)
	}
	if (File{}).Handle(id) != "" {
		t.Fatal("a handle without a salt")
	}
}
