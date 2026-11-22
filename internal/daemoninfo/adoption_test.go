package daemoninfo

import (
	"context"
	"testing"
)

// The daemon publishes whether it will follow new installs; a reader of an
// older daemon sees none.
func TestAdoptionIsPublished(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(dir, "example-build", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := Query(context.Background(), dir); err != nil || got.Adoption != "" {
		t.Fatalf("%+v %v", got, err)
	}
	s.SetAdoption("moved_link")
	if got, err := Query(context.Background(), dir); err != nil || got.Adoption != "moved_link" {
		t.Fatalf("%+v %v", got, err)
	}
	var none *Server
	none.SetAdoption("off") // a daemon without diagnostics
}
