package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestUpdateReturningAggregateDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,v TEXT,s SMALLINT,b BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,i INTERVAL)")
	execSQL(t, db, "INSERT INTO t(id,v) VALUES(1,'unchanged')")
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct {
		output string
		d      stuntgres.Error
	}{
		{"sum(zzzzz)", stuntgres.Error{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "33"}},
		{"sum(v)", stuntgres.Error{Code: "42883", Message: "function sum(text) does not exist", Hint: hint, Position: "29"}},
		{"public.sum(id)", stuntgres.Error{Code: "42883", Message: "function public.sum(integer) does not exist", Hint: hint, Position: "29"}},
		{"pg_catalog.sum(id)", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
		{"sum('7')", stuntgres.Error{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: "29"}},
		{"sum(NULL)", stuntgres.Error{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: "29"}},
		{"sum($1)", stuntgres.Error{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: "29"}},
	}
	for _, column := range []string{"id", "s", "b", "f", "g", "n", "i", "old.id", "new.id", "COALESCE(old.id,new.id)", "CAST(old.id AS BIGINT)", "id+1"} {
		cases = append(cases, struct {
			output string
			d      stuntgres.Error
		}{"sum(" + column + ")", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}})
	}
	for _, tc := range cases {
		for _, prefix := range []string{"", "/*é*/ "} {
			q := prefix + "UPDATE t SET id=2 RETURNING " + tc.output
			d := tc.d
			if prefix != "" {
				p, _ := strconv.Atoi(d.Position)
				d.Position = strconv.Itoa(p + 6)
			}
			_, err := db.Exec(q)
			typeCoreDiagnostic(t, err, d)
			rows, err := db.Query(q)
			if rows != nil {
				rows.Close()
				t.Fatal("invalid aggregate cursor")
			}
			typeCoreDiagnostic(t, err, d)
			held, err := db.Prepare(q)
			if held != nil {
				held.Close()
				t.Fatal("invalid aggregate handle")
			}
			typeCoreDiagnostic(t, err, d)
		}
	}
	for _, q := range []string{"SELECT sum(id) FROM t", "UPDATE t SET id=sum(id)", "INSERT INTO t(id) VALUES(sum(1))"} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(strings.Index(q, "sum") + 1)})
	}
	rows, err := db.Query("SELECT id,v FROM t")
	queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}, false)
}
