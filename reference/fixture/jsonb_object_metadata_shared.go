package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

// Computed metadata follows the public JSONB descriptor and pq codec contracts.
func JSONBObjectMetadata(t *testing.T, r *sql.Rows, pg bool, name string) {
	t.Helper()
	cs, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	ct := cs[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if ct.Name() != name || ct.DatabaseTypeName() != "JSONB" || ct.ScanType() != scan {
		t.Fatalf("metadata=%q/%q/%v want=%q/JSONB/%v", ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), name, scan)
	}
	if n, ok := ct.Length(); ok {
		t.Fatalf("unexpected length %d", n)
	}
	if p, s, ok := ct.DecimalSize(); ok {
		t.Fatalf("unexpected decimal %d,%d", p, s)
	}
	if n, ok := ct.Nullable(); n || ok {
		t.Fatalf("computed nullable=%v,%v want false,false", n, ok)
	}
}
