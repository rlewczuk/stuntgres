package stuntgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func TestMathSignatures(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-signatures")
	execSQL(t, db, "CREATE TABLE m(s SMALLINT,i INTEGER,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO m VALUES(9,9,9,9.00,2.5,3.5)")
	query := "SELECT mod(s,s),mod(s,i),mod(i,b),mod(i,n),round(s),round(i),round(b),round(r),round(d),round(n),scale(i),min_scale(b),trim_scale(s) FROM m"
	want := [][]any{{int64(0), int64(0), int64(0), "0.00", float64(9), float64(9), float64(9), float64(2), float64(4), "9", int64(0), int64(0), "9"}}
	wantTuples(t, db, query, want)
	s, err := db.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Query()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{"INT2", "INT4", "INT8", "NUMERIC", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "NUMERIC", "INT4", "INT4", "NUMERIC"} {
		if types[i].DatabaseTypeName() != typ {
			t.Fatal(i, types[i].DatabaseTypeName(), typ)
		}
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	got := make([]any, len(types))
	dst := make([]any, len(types))
	for i := range got {
		dst[i] = &got[i]
	}
	if err := rows.Scan(dst...); err != nil || !reflect.DeepEqual(got, want[0]) {
		t.Fatal(got, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	bound, err := db.Prepare("SELECT mod(b,$1) FROM m")
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	for _, tc := range []struct{ arg, want any }{{int64(4), int64(1)}, {nil, nil}, {int64(-4), int64(1)}} {
		var got any
		if err := bound.QueryRow(tc.arg).Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc, err)
		}
	}
}

func TestMathMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-metadata")
	execSQL(t, db, "CREATE TABLE m(n NUMERIC(8,4))")
	for _, q := range []string{"SELECT round(n,4),trim_scale(n),scale(n),round(4) FROM m", "SELECT round(NULL,4),trim_scale(NULL),scale(NULL),round(NULL)"} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []string{"NUMERIC", "NUMERIC", "INT4", "FLOAT8"} {
			if types[i].DatabaseTypeName() != want {
				t.Fatal(i, types[i])
			}
			if i < 2 {
				if _, _, ok := types[i].DecimalSize(); ok {
					t.Fatal("derived typmod leaked")
				}
				if types[i].ScanType() != reflect.TypeFor[string]() {
					t.Fatal(types[i].ScanType())
				}
			}
		}
		rows.Close()
	}
}

func TestMathDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-diagnostics")
	execSQL(t, db, "CREATE TABLE m(d DOUBLE PRECISION,n NUMERIC,b BIGINT)")
	lookupHint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ q, code, message, position, hint string }{
		{"SELECT mod('1','2')", "42725", "function mod(unknown, unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT trunc(NULL)", "42725", "function trunc(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT trunc('-0.5')", "42725", "function trunc(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT round(NULL,'oops')", "22P02", `invalid input syntax for type integer: "oops"`, "19", ""},
		{"SELECT mod($1,$2)", "42725", "function mod(unknown, unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT round(d,2) FROM m", "42883", "function round(double precision, integer) does not exist", "8", lookupHint},
		{"SELECT round(n,b) FROM m", "42883", "function round(numeric, bigint) does not exist", "8", lookupHint},
		{"SELECT scale(d) FROM m", "42883", "function scale(double precision) does not exist", "8", lookupHint},
		{"SELECT round('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "14", ""},
		{"SELECT scale('oops')", "22P02", `invalid input syntax for type numeric: "oops"`, "14", ""},
		{"SELECT round(1.0,'oops')", "22P02", `invalid input syntax for type integer: "oops"`, "18", ""},
		{"/*é*/ SELECT mod('1','2')", "42725", "function mod(unknown, unknown) is not unique", "14", "Could not choose a best candidate function. You might need to add explicit type casts."},
	} {
		_, err := db.Prepare(tc.q)
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v (%v)", tc.q, got, err)
		}
	}
	for _, q := range []string{"SELECT mod(1,0)", "SELECT mod(1.0,0.0)", "SELECT mod('Infinity',0.0)", "SELECT round(NULL,mod(1,0))"} {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal("prepare", q, err)
		}
		_, err = s.Exec()
		s.Close()
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v (%v)", q, got, err)
		}
	}
}

func TestMathCancellation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := db.ExecContext(ctx, "SELECT round(1.25,1)")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var diagnostic *stuntgres.Error
	if errors.As(err, &diagnostic) {
		t.Fatal("ordinary cancellation became SQL error", diagnostic)
	}
}
