package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func compareOpsRows(t *testing.T, rows *sql.Rows, err error, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal(meta, err)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal(i, meta[i])
		}
		if typ == "BOOL" && meta[i].ScanType() != reflect.TypeFor[bool]() {
			t.Fatal(meta[i])
		}
	}
	got := [][]any{}
	for rows.Next() {
		v, dst := make([]any, len(types)), make([]any, len(types))
		for i := range v {
			dst[i] = &v[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}
