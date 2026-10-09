package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func compareNullRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i], typ)
		}
		if typ == "BOOL" {
			if metadata[i].ScanType() != reflect.TypeFor[bool]() {
				t.Fatal(metadata[i])
			}
			if _, known := metadata[i].Nullable(); known {
				t.Fatal("invented computed nullability")
			}
		}
	}
	got := [][]any{}
	for rows.Next() {
		values := make([]any, len(names))
		destinations := make([]any, len(names))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}
