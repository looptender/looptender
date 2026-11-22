package bb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestListChildrenReadsIDsAndArchivedOnly(t *testing.T) {
	var many []string
	for i := 0; i < 250; i++ {
		many = append(many, fmt.Sprintf(`{"id":"thr_c%03d","archivedAt":1}`, i))
	}
	big := "[" + strings.Join(append(many, `{"id":"thr_live"}`), ",") + "]"
	huge := "[" + strings.TrimSuffix(strings.Repeat(`{"id":"thr_x"},`, MaxListedChildren+1), ",") + "]"
	for _, tc := range []struct {
		name, out string
		fail      bool
		want      []ListedChild
		wantErr   bool
	}{
		{"none", `[]`, false, []ListedChild{}, false},
		{"archived and deleted marked", `[{"id":"thr_a"},{"id":"thr_b","archivedAt":1},{"id":"thr_c","deletedAt":2},{"id":"thr_a"}]`, false,
			[]ListedChild{{ID: "thr_a"}, {ID: "thr_b", Archived: true}, {ID: "thr_c", Archived: true}}, false},
		{"the parent itself is not a child", `[{"id":"thr_parent"}]`, false, nil, true},
		{"unusable id", `[{"id":"--all"}]`, false, nil, true},
		{"null is not an empty listing", `null`, false, nil, true},
		{"too large to read", huge, false, nil, true},
		{"many archived", big, false, nil, false},
		{"error envelope", `{"ok":false,"error":{"code":"thread_not_found"}}`, true, nil, true},
		{"command failure", `[]`, true, nil, true},
	} {
		a, _ := newFake(`{}`, `[]`, false)
		a.run = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
			if stdin != "" || !reflect.DeepEqual(args, []string{"thread", "list", "--parent-thread", "thr_parent", "--include-hidden", "--json"}) {
				t.Fatalf("unexpected command %v", args)
			}
			if tc.fail {
				return []byte(tc.out), errors.New("exit 1")
			}
			return []byte(tc.out), nil
		}
		got, err := a.ListChildren(context.Background(), "thr_parent")
		if tc.name == "many archived" {
			if err != nil || len(got) != 251 || got[250] != (ListedChild{ID: "thr_live"}) {
				t.Errorf("%s: %d %v", tc.name, len(got), err)
			}
			continue
		}
		if (err != nil) != tc.wantErr || (!tc.wantErr && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("%s: %v %v", tc.name, got, err)
		}
	}
}

// SetParent runs exactly one update that changes only the parent.
func TestSetParentChangesOnlyTheParent(t *testing.T) {
	var ran [][]string
	a, _ := newFake(`{}`, `[]`, false)
	a.run = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		ran = append(ran, args)
		return []byte(`{"ok":true}`), nil
	}
	if err := a.SetParent(context.Background(), "thr_child", "thr_new"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, [][]string{{"thread", "update", "thr_child", "--parent-thread", "thr_new", "--json"}}) {
		t.Fatalf("%v", ran)
	}
	a.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"ok":false,"error":{"code":"thread_not_found"}}`), errors.New("exit 1")
	}
	if err := a.SetParent(context.Background(), "thr_child", "thr_new"); err == nil || !strings.Contains(err.Error(), "thread_not_found") {
		t.Fatal(err)
	}
	a.run = func(context.Context, string, ...string) ([]byte, error) { t.Fatal("ran bb"); return nil, nil }
	for _, bad := range [][2]string{{"thr_a", "thr_a"}, {"--all", "thr_b"}, {"thr_a", ""}} {
		if err := a.SetParent(context.Background(), bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
