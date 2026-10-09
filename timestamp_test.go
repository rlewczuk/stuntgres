package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func timestampCheckRows(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	gotNames, e := rows.Columns()
	if e != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, e)
	}
	metadata, e := rows.ColumnTypes()
	if e != nil {
		t.Fatal(e)
	}
	for i, n := range types {
		if metadata[i].DatabaseTypeName() != n {
			t.Fatal(metadata[i])
		}
		if n == "TIMESTAMP" && metadata[i].ScanType() != reflect.TypeFor[time.Time]() {
			t.Fatal(metadata[i])
		}
	}
	var got [][]any
	for rows.Next() {
		row := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range row {
			dst[i] = &row[i]
		}
		if e = rows.Scan(dst...); e != nil {
			t.Fatal(e)
		}
		for _, v := range row {
			if tm, ok := v.(time.Time); ok {
				_, off := tm.Zone()
				if off != 0 {
					t.Fatal("timestamp output offset", tm)
				}
			}
		}
		got = append(got, row)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
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
func timestampQuery(t *testing.T, db *sql.DB, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	rows, e := db.Query(q, args...)
	if e != nil {
		t.Fatal(q, e)
	}
	timestampCheckRows(t, rows, names, types, want)
}
