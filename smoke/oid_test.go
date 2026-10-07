package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func TestOIDSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	check := func(q string, want []any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if types[0].DatabaseTypeName() != "OID" || types[0].ScanType() != reflect.TypeFor[int64]() {
			t.Fatal(types)
		}
		got := make([]any, 0)
		for rows.Next() {
			var v any
			if err = rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
	diagnostic := func(err error, code, message, position string) {
		t.Helper()
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v) want %#v", got, err, want)
		}
	}
	exec("CREATE TABLE o(v OID DEFAULT '4294967295'::oid)")
	exec("INSERT INTO o VALUES('0'),('2147483648'),('4294967295'),(NULL)")
	stmt, err := db.PrepareContext(ctx, "INSERT INTO o VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, v := range []any{nil, "4294967295", int64(-1)} {
		if _, err = stmt.ExecContext(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	want := []any{int64(0), int64(2147483648), int64(4294967295), int64(4294967295), int64(4294967295), nil, nil}
	check("SELECT v FROM o ORDER BY v", want)
	var equal bool
	var signed, unsigned int64
	if err = db.QueryRowContext(ctx, "SELECT v=(-1)::int4,v::int4,CAST(v AS int8) FROM o WHERE v='4294967295'").Scan(&equal, &signed, &unsigned); err != nil || !equal || signed != -1 || unsigned != 4294967295 {
		t.Fatal(equal, signed, unsigned, err)
	}
	var distinct, notDistinct bool
	if err = db.QueryRowContext(ctx, "SELECT v IS DISTINCT FROM (-1)::int4,(-1)::int4 IS NOT DISTINCT FROM v FROM o WHERE v='4294967295'").Scan(&distinct, &notDistinct); err != nil || distinct || !notDistinct {
		t.Fatal(distinct, notDistinct, err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO o VALUES('1'),('4294967296')")
	diagnostic(err, "22003", `value "4294967296" is out of range for type oid`, "28")
	check("SELECT v FROM o ORDER BY v", want)
	_, err = db.ExecContext(ctx, "INSERT INTO o VALUES((-1)::int8::oid)")
	diagnostic(err, "22003", "OID out of range", "")
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
	check("SELECT v FROM o ORDER BY v", want)
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
}
