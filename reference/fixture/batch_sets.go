package fixture

import (
	"database/sql"
	"slices"
	"testing"
)

type BatchSet struct {
	Columns, Types []string
	Rows           [][]any
	Bag            bool
}

func BatchReadSet(t *testing.T, r *sql.Rows, w BatchSet) [][]any {
	t.Helper()
	cols, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cols, w.Columns) {
		t.Fatalf("columns=%v want=%v", cols, w.Columns)
	}
	types, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	gotTypes := make([]string, len(types))
	for i, v := range types {
		gotTypes[i] = v.DatabaseTypeName()
	}
	if !slices.Equal(gotTypes, w.Types) {
		t.Fatalf("types=%v want=%v", gotTypes, w.Types)
	}
	values := make([][]any, 0)
	for r.Next() {
		row := make([]any, len(cols))
		dst := make([]any, len(cols))
		for i := range row {
			dst[i] = &row[i]
		}
		if err := r.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			if bytes, ok := v.([]byte); ok {
				row[i] = string(bytes)
			}
		}
		values = append(values, row)
	}
	if _, ok := CompareRows(values, w.Rows, w.Bag); !ok {
		t.Fatalf("rows=%#v want=%#v", values, w.Rows)
	}
	return values
}
func BatchSets(t *testing.T, r *sql.Rows, w ...BatchSet) {
	t.Helper()
	defer r.Close()
	for i, set := range w {
		if i > 0 && !r.NextResultSet() {
			t.Fatalf("missing set %d: %v", i, r.Err())
		}
		BatchReadSet(t, r, set)
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if r.NextResultSet() || r.Err() != nil {
		t.Fatalf("extra set/error: %v", r.Err())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}
