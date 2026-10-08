package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestMathScalarSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("math-scalar-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, tc := range []struct {
		query, typ string
		want       any
	}{
		{"SELECT abs(-17.400)", "NUMERIC", "17.400"}, {"SELECT ceil(-42.8)", "NUMERIC", "-42"}, {"SELECT ceiling(-42.8)", "NUMERIC", "-42"}, {"SELECT floor(-42.8)", "NUMERIC", "-43"},
		{"SELECT div(9007199254740993,2)", "NUMERIC", "4503599627370496"}, {"SELECT exp(1." + strings.Repeat("0", 70) + ")", "NUMERIC", "2.7182818284590452353602874713526624977572470936999595749669676277240766"}, {"SELECT factorial(20)", "NUMERIC", "2432902008176640000"},
		{"SELECT abs(NULL)", "FLOAT8", nil}, {"SELECT factorial(NULL)", "NUMERIC", nil},
	} {
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				s, e := db.PrepareContext(ctx, tc.query)
				if e != nil {
					t.Fatal(tc.query, e)
				}
				rows, err = s.QueryContext(ctx)
				s.Close()
			} else {
				rows, err = db.QueryContext(ctx, tc.query)
			}
			if err != nil {
				t.Fatal(tc.query, err)
			}
			types, e := rows.ColumnTypes()
			if e != nil || len(types) != 1 || types[0].DatabaseTypeName() != tc.typ {
				t.Fatal(tc.query, types, e)
			}
			if tc.typ == "NUMERIC" {
				if _, _, ok := types[0].DecimalSize(); ok {
					t.Fatal("derived typmod")
				}
			}
			if !rows.Next() {
				t.Fatal(tc.query, rows.Err())
			}
			var got any
			if e := rows.Scan(&got); e != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(tc.query, got, tc.want, e)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
		}
	}
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	exec("CREATE TABLE widths(s SMALLINT,r REAL)")
	exec("INSERT INTO widths VALUES(-17,-17.5)")
	rows, err := db.Query("SELECT abs(s),abs(r) FROM widths")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || types[0].DatabaseTypeName() != "INT2" || types[1].DatabaseTypeName() != "FLOAT4" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var i int64
	var r float64
	if err := rows.Scan(&i, &r); err != nil || i != 17 || r != 17.5 {
		t.Fatal(i, r, err)
	}
	rows.Close()
	s, err := db.Prepare("SELECT div($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct{ a, want any }{{"9007199254740993", "4503599627370496"}, {nil, nil}, {"-9.00", "-4"}, {"9.00", "4"}} {
		var got any
		if err := s.QueryRow(tc.a, "2").Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc, err)
		}
	}
	for _, tc := range []struct{ q, code, message string }{{"SELECT div(1,0)", "22012", "division by zero"}, {"SELECT factorial(-1)", "22003", "factorial of a negative number is undefined"}} {
		s, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal("Prepare evaluated provider", err)
		}
		_, err = s.Exec()
		s.Close()
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err)
		}
	}
	exec("CREATE TABLE source(id INT,n NUMERIC,d NUMERIC)")
	exec("INSERT INTO source VALUES(1,-9.00,4),(2,9.00,0)")
	exec("CREATE TABLE stored(n NUMERIC)")
	for _, q := range []string{"UPDATE source SET n=div(n,d)", "INSERT INTO stored SELECT div(n,d) FROM source"} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("atomic failure succeeded", q)
		}
	}
	var n string
	if err := db.QueryRow("SELECT n FROM source WHERE id=1").Scan(&n); err != nil || n != "-9.00" {
		t.Fatal(n, err)
	}
	var count int64
	if err := db.QueryRow("SELECT count(*) FROM stored").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	exec("INSERT INTO stored SELECT abs(n) FROM source")
	exec("CREATE VIEW scalar_view AS SELECT ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,div(n,4) AS q,exp(0.0) AS e,factorial(5) AS fac FROM source")
	held, err := db.Query("SELECT n FROM stored")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var c, cc, f, q, e, fac string
	if err := db.QueryRow("SELECT c,cc,f,q,e,fac FROM scalar_view").Scan(&c, &cc, &f, &q, &e, &fac); err != nil || c != "-9" || cc != "-9" || f != "-9" || q != "-2" || e != "1.0000000000000000" || fac != "120" {
		t.Fatal(c, cc, f, q, e, fac, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	if err := held.Scan(&n); err != nil || n != "9.00" {
		t.Fatal(n, err)
	}
	oldTypes, err := held.ColumnTypes()
	if err != nil || oldTypes[0].DatabaseTypeName() != "NUMERIC" {
		t.Fatal(oldTypes, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("roundtrip", err)
	}
}
