package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres"
)

func TestMathSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("math-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, tc := range []struct {
		query, typ string
		want       any
	}{
		{"SELECT round(2.5)", "NUMERIC", "3"},
		{"SELECT round(-2.5)", "NUMERIC", "-3"},
		{"SELECT round(-1.25,1)", "NUMERIC", "-1.3"},
		{"SELECT round(1500,-3)", "NUMERIC", "2000"},
		{"SELECT round(1.23,4)", "NUMERIC", "1.2300"},
		{"SELECT round(4)", "FLOAT8", float64(4)},
		{"SELECT round('2.5')", "FLOAT8", float64(2)},
		{"SELECT trunc(-1.25,1)", "NUMERIC", "-1.2"},
		{"SELECT trunc(-1500,-3)", "NUMERIC", "-1000"},
		{"SELECT scale(8.4100)", "INT4", int64(4)},
		{"SELECT min_scale(8.4100)", "INT4", int64(2)},
		{"SELECT trim_scale(8.4100)", "NUMERIC", "8.41"},
		{"SELECT trim_scale(0.0000)", "NUMERIC", "0"},
		{"SELECT sign(-13.000)", "NUMERIC", "-1"},
		{"SELECT sign('NaN')", "FLOAT8", float64(0)},
		{"SELECT mod(-9,4)", "INT4", int64(-1)},
		{"SELECT mod(-9.00,4.0)", "NUMERIC", "-1.00"},
		{"SELECT mod(9007199254740993.25,2.0)", "NUMERIC", "1.25"},
		{"SELECT mod(1.2300,'Infinity')", "NUMERIC", "1.2300"},
		{"SELECT mod('NaN',0.0)", "NUMERIC", "NaN"},
		{"SELECT round(NULL,2)", "NUMERIC", nil},
		{"SELECT scale(NULL)", "INT4", nil},
		{"SELECT min_scale('NaN')", "INT4", nil},
		{"SELECT mod(1,NULL)", "INT4", nil},
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
				t.Fatalf("%s types %v: %v", tc.query, types, e)
			}
			if !rows.Next() {
				t.Fatal(tc.query, rows.Err())
			}
			var got any
			if e = rows.Scan(&got); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s prepared=%v: %#v want %#v", tc.query, prepared, got, tc.want)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(tc.query, rows.Err())
			}
			rows.Close()
		}
	}
	for _, q := range []string{"CREATE TABLE math_float(d DOUBLE PRECISION)", "INSERT INTO math_float VALUES('-0.5')"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"SELECT round('-0.5')", "SELECT trunc(d) FROM math_float"} {
		var got float64
		if err := db.QueryRowContext(ctx, q).Scan(&got); err != nil || got != 0 || !math.Signbit(got) {
			t.Fatalf("%s: %v %v", q, got, err)
		}
	}
	s, err := db.PrepareContext(ctx, "SELECT round($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		input any
		want  any
	}{{"1.25", "1.3"}, {"-1.25", "-1.3"}, {nil, nil}, {"1.24", "1.2"}} {
		var got any
		if err := s.QueryRowContext(ctx, tc.input, 1).Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("fresh bind: %#v %v", got, err)
		}
	}
	zero, err := db.PrepareContext(ctx, "SELECT mod(1,0)")
	if err != nil {
		t.Fatal("runtime error during prepare", err)
	}
	defer zero.Close()
	_, err = zero.ExecContext(ctx)
	var diagnostic *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("diagnostic %#v (%v)", diagnostic, err)
	}
	for _, q := range []string{
		"CREATE TABLE source(n NUMERIC)",
		"INSERT INTO source VALUES(1.23)",
		"CREATE TABLE stored(padded NUMERIC,trimmed NUMERIC)",
		"INSERT INTO stored SELECT round(n,4),trim_scale(n) FROM source",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
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
	var padded, trimmed string
	if err := db.QueryRowContext(ctx, "SELECT padded,trimmed FROM stored").Scan(&padded, &trimmed); err != nil || padded != "1.2300" || trimmed != "1.23" {
		t.Fatal(padded, trimmed, err)
	}
}
