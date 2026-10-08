package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestMathScalarLookupAndLifecycle(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-scalar-lookup")
	execSQL(t, db, "CREATE TABLE m(n NUMERIC,r REAL,d DOUBLE PRECISION,i INTEGER)")
	execSQL(t, db, "CREATE SCHEMA other")
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ q, message string }{
		{"SELECT div(d,d) FROM m", "function div(double precision, double precision) does not exist"},
		{"SELECT div(r,r) FROM m", "function div(real, real) does not exist"},
		{"SELECT factorial(n) FROM m", "function factorial(numeric) does not exist"},
		{"SELECT factorial(d) FROM m", "function factorial(double precision) does not exist"},
		{"SELECT other.abs(1)", "function other.abs(integer) does not exist"},
	} {
		_, err := db.Prepare(tc.q)
		mathScalarDiagnostic(t, err, "42883", tc.message, "8", hint)
	}
	for _, q := range []string{"SELECT abs(n) AS magnitude,ceil(n) AS upper,ceiling(n) AS alias,floor(n) AS lower,exp(n) AS growth,div(n,1) AS quotient,factorial(i) AS product FROM m", "SELECT pg_catalog.abs(n),pg_catalog.ceil(n),pg_catalog.ceiling(n),pg_catalog.floor(n),pg_catalog.exp(n),pg_catalog.div(n,1),pg_catalog.factorial(i) FROM m"} {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal(q, err)
		}
		rows, err := s.Query()
		s.Close()
		if err != nil {
			t.Fatal(q, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 7 {
			t.Fatal(types, err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "NUMERIC" {
				t.Fatal(typ)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
	}
	for _, q := range []string{"SELECT div(1,0) FROM m", "SELECT factorial(-1) FROM m", "SELECT exp(5999.9999999999999999999999) FROM m"} {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal("Prepare evaluated", q, err)
		}
		_, err = s.Exec()
		s.Close()
		if q == "SELECT div(1,0) FROM m" {
			mathScalarDiagnostic(t, err, "22012", "division by zero", "", "")
		} else if q == "SELECT factorial(-1) FROM m" {
			mathScalarDiagnostic(t, err, "22003", "factorial of a negative number is undefined", "", "")
		} else {
			mathScalarDiagnostic(t, err, "22003", "value overflows numeric format", "", "")
		}
	}
	execSQL(t, db, "INSERT INTO m VALUES(NULL,1,1,9)")
	wantTuples(t, db, "SELECT div(NULL,div(i,0)),div(n,0) FROM m", [][]any{{nil, nil}})
	_, err := db.Exec("SELECT div(n,div(i,0)) FROM m")
	mathScalarDiagnostic(t, err, "22012", "division by zero", "", "")
}
