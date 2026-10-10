package smoke_test

import (
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func alterDropSmokePool(t *testing.T, notices *[]stuntgres.Error) *sql.DB {
	t.Helper()
	base, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { *notices = append(*notices, n) }))
	t.Cleanup(func() { db.Close() })
	return db
}
func alterDropSmokeExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}
func alterDropSmokeError(t *testing.T, err error, code, message, position string) {
	t.Helper()
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err, want)
	}
}
func alterDropSmokeRows(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, c := range metadata {
		scan := reflect.TypeFor[int64]()
		switch types[i] {
		case "TEXT", "VARCHAR", "NUMERIC":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "FLOAT8":
			scan = reflect.TypeFor[float64]()
		}
		if c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
			t.Fatal(c)
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range row {
			targets[i] = &row[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}
