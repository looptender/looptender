package main

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/health"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/report"
	"github.com/looptender/looptender/internal/state"
)

// adoptSchemaRoots are the records a build rewrites in a loop's state
// directory: state.json, control.json, the incident journal, sends.json,
// health.json, deliveries.json and goal.json. Not covered: new values of an
// existing string field (an enum), which an older build may misread without
// any field changing, and records an older build only reads or appends to
// (runs.jsonl, the run graph, build-adoption.json).
var adoptSchemaRoots = []reflect.Type{
	reflect.TypeOf(state.File{}), reflect.TypeOf(control.File{}), reflect.TypeOf(incident.Journal{}),
	reflect.TypeOf(state.Ledger{}), reflect.TypeOf(health.Registry{}), reflect.TypeOf(report.DeliveryHistory{}), reflect.TypeOf(control.Goal{}),
}

// adoptSchemaFingerprints holds, for each adoptStateFormat, the fingerprint
// of the records an earlier build would rewrite without understanding a
// field it does not know (adoptSchemaRoots). A daemon never switches to a build with a lower state format,
// so a field added to any of them must come with a new state format, or a
// build without the field could be switched to and drop it.
var adoptSchemaFingerprints = map[int]string{
	1: "d4e5947f3f88eeb97f1c01720852963f0200212620c05e88b857eb776a7827a2",
	// 2: owner hand-off moves: state.json gained pendingOwnerMove and
	// control.json's owner gained followed.
	2: "66e6b2bc08152cee8471ad61274cad7b1ffdfb449f1716a2f62d4660e6ceb534",
	// 3: state.json gained childMove (moving a left conversation's child
	// threads) and limitWaitReport's reason and reportSection.
	3: "9a7aa1130ff38ab6bbcb65b4d0d80e1b52e4ce42304954aa922f44d685fe30f5",
	// 4: state.json gained movedAt and movedControlAt (the mark transfer apply
	// leaves on its own hold; kept out of control.json so older builds can
	// still pause a moved loop).
	4: "4c0b2fe163cafb87ef954ed2590ad21a69ac214ab5062c473a96609572d5bcb5",
	// 5: state.json's childMove gained transfer and left (a child move made
	// by transfer apply, or its choice to leave the children).
	5: "dc1f9bcc270d8fff7841264b2d32bb55a3865caf613828e0983cd77cf7efec9a",
	// 6: state.json gained childSuccessors and childSuccessorsCreated
	// (successor threads replacing child lanes blocked on another provider)
	// and childMove's movedIds.
	6: "caa99878e54f0d0a3dde2da240155cd6b9b23db79beab3d6e5429b342c9ed73b",
	// 7: state.json gained usageLimit (the provider usage limit the last
	// check found, with its window and the Claude directory it ran under).
	7: "bf1e793259de9f74d358f61bd15727d924ae7ab854a98f1964c86fb5f5735508",
}

// schemaFingerprint lists every JSON field reachable from t, by path, with
// its kind, and hashes the sorted list.
func schemaFingerprint(roots ...reflect.Type) (string, []string) {
	var fields []string
	seen := map[reflect.Type]bool{}
	var walk func(prefix string, t reflect.Type)
	walk = func(prefix string, t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			if t.Kind() == reflect.Map {
				prefix += "{}"
			} else if t.Kind() != reflect.Pointer {
				prefix += "[]"
			}
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || !strings.HasPrefix(t.PkgPath(), "github.com/looptender/") {
			fields = append(fields, prefix+":"+t.Kind().String())
			return
		}
		if seen[t] {
			fields = append(fields, prefix+":"+t.Name())
			return
		}
		seen[t] = true
		defer delete(seen, t)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if f.Anonymous && f.Tag.Get("json") == "" {
				walk(prefix, f.Type)
				continue
			}
			walk(prefix+"."+name, f.Type)
		}
	}
	for _, r := range roots {
		walk(r.String(), r)
	}
	sort.Strings(fields)
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(sum[:]), fields
}

func TestAdoptStateFormatFollowsTheSchemas(t *testing.T) {
	got, fields := schemaFingerprint(adoptSchemaRoots...)
	if want := adoptSchemaFingerprints[adoptStateFormat]; got != want {
		t.Fatalf("a record a build rewrites in the state directory changed (%d fields). An earlier build would drop what it does not know, "+
			"so raise adoptStateFormat in adopt_build.go and record this fingerprint for it in adoptSchemaFingerprints: %q", len(fields), got)
	}
	// Each format has its own fingerprint: a rise without a change, or a
	// change recorded under an old number, is caught too.
	seen := map[string]int{}
	for format, fp := range adoptSchemaFingerprints {
		if other, ok := seen[fp]; ok {
			t.Fatalf("formats %d and %d share a fingerprint", format, other)
		}
		seen[fp] = format
		if format > adoptStateFormat {
			t.Fatalf("fingerprint recorded for format %d above adoptStateFormat %d", format, adoptStateFormat)
		}
	}
}

type schemaBefore struct {
	A    string `json:"a"`
	Nest *struct {
		B []int `json:"b"`
	} `json:"nest"`
}

type schemaAfter struct {
	A    string `json:"a"`
	Nest *struct {
		B []int `json:"b"`
		C bool  `json:"c,omitempty"`
	} `json:"nest"`
}

// A field added anywhere, even nested and optional, changes the
// fingerprint.
func TestSchemaFingerprintSeesANestedField(t *testing.T) {
	before, _ := schemaFingerprint(reflect.TypeOf(schemaBefore{}))
	after, fields := schemaFingerprint(reflect.TypeOf(schemaAfter{}))
	again, _ := schemaFingerprint(reflect.TypeOf(schemaBefore{}))
	if before == after || before != again {
		t.Fatalf("fingerprint blind or unstable: %v", fields)
	}
}
