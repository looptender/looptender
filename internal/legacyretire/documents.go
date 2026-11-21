//go:build darwin || linux

package legacyretire

import (
	"bytes"
	"encoding/json"
	"golang.org/x/sys/unix"
	"path/filepath"
	"reflect"
	"strings"
)

// readDocument rejects duplicate/unknown/case-alias keys and null scalar
// coercions recursively, while allowing whitespace and object-key ordering.
func readDocument(path string, out any) error {
	raw, _, e := snapshotPath(path, 32768, true)
	if e != nil {
		return e
	}
	if _, e = object(raw); e != nil {
		return e
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrUnavailable
	}
	canonical, e := json.Marshal(out)
	if e != nil {
		return ErrUnavailable
	}
	decode := func(b []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		e := d.Decode(&v)
		return v, e
	}
	actual, e := decode(raw)
	if e != nil {
		return ErrUnavailable
	}
	expected, e := decode(canonical)
	if e != nil || !reflect.DeepEqual(actual, expected) {
		return ErrUnavailable
	}
	return nil
}
func ReadSelection(path string) (Selection, error) {
	var s Selection
	e := readDocument(path, &s)
	return s, e
}
func ReadTicket(path string) (Ticket, error) { var t Ticket; e := readDocument(path, &t); return t, e }

// WriteTicket exclusively publishes the prepared ticket outside its attempt
// directory. Failure leaves all partial evidence; no implicit re-preparation.
func WriteTicket(path string, t Ticket) error {
	rel, e := filepath.Rel(t.Root, path)
	if e != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return ErrUnavailable
	}
	fd, _, e := openRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if _, e = create(fd, filepath.Base(path), b, hooks{}); e != nil {
		return e
	}
	return directorySync(fd, "ticket-directory-sync", hooks{})
}
