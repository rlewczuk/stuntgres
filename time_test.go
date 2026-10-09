package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func timeCheckRows(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i])
		}
		if typ == "TIME" && metadata[i].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(metadata[i])
		}
	}
	var got [][]any
	for rows.Next() {
		row, dst := make([]any, len(names)), make([]any, len(names))
		for i := range row {
			dst[i] = &row[i]
		}
		if err = rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("rows %#v want %#v", got, want)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("row %#v want %#v", got[i], want[i])
		}
	}
}

func timeQuery(t *testing.T, db *sql.DB, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	timeCheckRows(t, rows, names, types, want)
}
