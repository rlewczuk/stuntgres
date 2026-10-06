package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestDateSmoke(t *testing.T) {
	ctx := context.Background()
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, count int64, args ...any) {
		t.Helper()
		r, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != count {
			t.Fatal(n, err)
		}
	}
	check := func(rows *sql.Rows, want []any) {
		t.Helper()
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(meta, err)
		}
		var got []any
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if rows.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, rows.Err())
		}
	}
	query := func(q string, want []any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		check(rows, want)
	}
	diag := func(err error, want stuntgres.Error) {
		t.Helper()
		want.Severity = "ERROR"
		want.SeverityUnlocalized = "ERROR"
		var got *stuntgres.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatal(got, err, want)
		}
	}
	exec("CREATE TABLE d(v DATE)", 0)
	exec("INSERT INTO d VALUES($1)", 1, "January 8, 1999")
	stmt, err := db.PrepareContext(ctx, "INSERT INTO d VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{[]byte("2000-02-29"), time.Date(2000, 1, 1, 0, 1, 0, 0, time.FixedZone("east", 14*3600)), nil, "4714-11-24 BC", "0001-02-29 BC", "5874897-12-31", "-infinity", "infinity"} {
		r, err := stmt.ExecContext(ctx, arg)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	want := []any{"-infinity", "4714-11-24 BC", "0001-02-29 BC", "1999-01-08", "2000-01-01", "2000-02-29", "5874897-12-31", "infinity", nil}
	query("SELECT v FROM d ORDER BY v", want)
	query("SELECT DATE 'epoch' FROM d WHERE v='2000-02-29'", []any{"1970-01-01"})
	var comparison bool
	if err := db.QueryRowContext(ctx, "SELECT v<DATE '2000-03-01' FROM d WHERE v=$1", "2000-02-29").Scan(&comparison); err != nil || !comparison {
		t.Fatal(comparison, err)
	}
	for _, tc := range []struct{ q, code, message, pos string }{{"INSERT INTO d VALUES('2000-02-29'),('bad')", "22007", `invalid input syntax for type date: "bad"`, "37"}, {"INSERT INTO d VALUES('5874898-01-01')", "22008", `date out of range: "5874898-01-01"`, "22"}, {"CREATE TABLE bad(v DATE(3))", "42601", `type modifier is not allowed for type "date"`, "20"}} {
		_, err := db.ExecContext(ctx, tc.q)
		diag(err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.pos})
	}
	query("SELECT v FROM d ORDER BY v", want)
	_, err = db.ExecContext(ctx, "CREATE TABLE bad_float(v DATE(3.0))")
	diag(err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "26"})
	// Successful reuse of the name proves the failed CREATE published nothing.
	exec("CREATE TABLE bad_float(v DATE)", 0)
	exec("CREATE TABLE i(v INTEGER)", 0)
	exec("INSERT INTO i VALUES(7)", 1)
	_, err = db.ExecContext(ctx, "INSERT INTO i VALUES(DATE '2000-01-01')")
	diag(err, stuntgres.Error{Code: "42804", Message: `column "v" is of type integer but expression is of type date`, Hint: "You will need to rewrite or cast the expression.", Position: "27"})
	integers, err := db.QueryContext(ctx, "SELECT v FROM i ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer integers.Close()
	var sentinel int64
	if !integers.Next() {
		t.Fatal("INTEGER sentinel missing", integers.Err())
	}
	if err := integers.Scan(&sentinel); err != nil || sentinel != 7 {
		t.Fatal(sentinel, err)
	}
	if integers.Next() || integers.Err() != nil {
		t.Fatal("typed assignment changed INTEGER rows", integers.Err())
	}
	if err := integers.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := db.QueryContext(ctx, "SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(old, want)
	query("SELECT v FROM d ORDER BY v", want)
}
