package smoke_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func aggregateSmokeRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i], typ)
		}
	}
	got := [][]any{}
	for rows.Next() {
		values := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range values {
			dst[i] = &values[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	used := make([]bool, len(want))
	for _, row := range got {
		found := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(row, expected) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatal("missing bag occurrence", row, got, want)
		}
	}
}
