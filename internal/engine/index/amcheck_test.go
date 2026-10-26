package index

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestAmcheckRootDescent(t *testing.T) {
	entries := []storage.IndexEntry{{Row: 1, Version: 1}, {Row: 2, Version: 2}, {Row: 3, Version: 3}}
	capture := AmcheckCapture{Index: &catalog.Relation{ObjectName: "i"}, Root: &storage.IndexRoot{Entries: entries}}
	compare := func(ctx context.Context, a, b storage.IndexEntry) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if a.Row < b.Row {
			return -1, nil
		}
		if a.Row > b.Row {
			return 1, nil
		}
		return 0, nil
	}
	if err := capture.checkDescent(context.Background(), compare); err != nil {
		t.Fatal(err)
	}
	// A genuinely inconsistent comparison cannot find even its own tuple.
	inconsistent := func(context.Context, storage.IndexEntry, storage.IndexEntry) (int, error) { return -1, nil }
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX002", Message: `could not find tuple using search from root page in index "i"`}
	err := capture.checkDescent(context.Background(), inconsistent)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		found, err := amcheckSearch(context.Background(), entries, entry, compare)
		if err != nil || entries[found].Version != entry.Version {
			t.Fatal(found, err)
		}
	}
}
func TestAmcheckCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries := []storage.IndexEntry{{Row: 1, Version: 1}, {Row: 2, Version: 2}}
	capture := AmcheckCapture{Index: &catalog.Relation{ObjectName: "i"}, Root: &storage.IndexRoot{Entries: entries}}
	entered := false
	compare := func(ctx context.Context, a, b storage.IndexEntry) (int, error) {
		entered = true
		cancel()
		return 0, ctx.Err()
	}
	if err := capture.checkDescent(ctx, compare); !entered || !errors.Is(err, context.Canceled) {
		t.Fatal("active comparison cancellation", entered, err)
	}
	if !reflect.DeepEqual(entries, []storage.IndexEntry{{Row: ids.RowID(1), Version: ids.VersionID(1)}, {Row: ids.RowID(2), Version: ids.VersionID(2)}}) {
		t.Fatal("search mutated authority")
	}
}
