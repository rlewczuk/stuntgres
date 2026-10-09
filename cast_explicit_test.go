package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func castExplicitMetadata(t *testing.T, rows *sql.Rows, names, types []string) {
	t.Helper()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatal(got, names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != len(types) {
		t.Fatal(meta)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if typ == "TEXT" {
			scan = reflect.TypeFor[string]()
		} else if typ == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		if meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != scan {
			t.Fatal(meta[i], typ, scan)
		}
	}
}
