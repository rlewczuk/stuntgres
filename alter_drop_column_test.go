package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"
)

// Unlike tableCoreMetadata's empty-table contract, these results retain rows.
func alterDropMetadata(t *testing.T, db *sql.DB, prepared bool, q string, names, types []string, nullable []bool, count int) {
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
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, c := range metadata {
		scan := reflect.TypeFor[int64]()
		if types[i] == "TEXT" {
			scan = reflect.TypeFor[string]()
		} else if types[i] == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		value, known := c.Nullable()
		if c.DatabaseTypeName() != types[i] || c.ScanType() != scan || !known || value != nullable[i] {
			t.Fatal(c, value, known)
		}
	}
	observed := 0
	for rows.Next() {
		values := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		observed++
	}
	if err = rows.Err(); err != nil || observed != count {
		t.Fatal(observed, count, err)
	}
}
