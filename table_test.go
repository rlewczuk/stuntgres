package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func tableCoreExec(t *testing.T, db *sql.DB, prepared bool, q string, count int64, args ...any) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer s.Close()
		result, err = s.Exec(args...)
	} else {
		result, err = db.Exec(q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	got, err := result.RowsAffected()
	if err != nil || got != count {
		t.Fatal(q, got, err)
	}
}
func tableCoreMetadata(t *testing.T, db *sql.DB, prepared bool, q string, names, types []string, nullable []bool) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		rows, err = s.Query()
	} else {
		rows, err = db.Query(q)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatal(got, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, n := range types {
		c := metadata[i]
		v, known := c.Nullable()
		var scan reflect.Type
		switch n {
		case "INT2", "INT4", "INT8":
			scan = reflect.TypeFor[int64]()
		case "TEXT":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if c.DatabaseTypeName() != n || c.ScanType() != scan || !known || v != nullable[i] {
			t.Fatal(i, c, v, known)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("expected empty shape", rows.Err())
	}
}
