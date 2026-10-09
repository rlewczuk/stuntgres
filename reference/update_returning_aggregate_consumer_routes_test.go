package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceUpdateReturningAggregateDiagnosticsConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,v TEXT,s SMALLINT,b BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,i INTERVAL)")
		updateReturningReferenceExec(t, db, "INSERT INTO t(id,v) VALUES(1,'unchanged')")
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		cases := []struct {
			output string
			d      referenceDiagnostic
		}{
			{"sum(zzzzz)", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "33"}},
			{"sum(v)", referenceDiagnostic{Code: "42883", Message: "function sum(text) does not exist", Hint: hint, Position: "29"}},
			{"public.sum(id)", referenceDiagnostic{Code: "42883", Message: "function public.sum(integer) does not exist", Hint: hint, Position: "29"}},
			{"pg_catalog.sum(id)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
			{"sum('7')", referenceDiagnostic{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: "29"}},
			{"sum(NULL)", referenceDiagnostic{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: "29"}},
			{"sum($1)", referenceDiagnostic{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: "29"}},
		}
		for _, column := range []string{"id", "s", "b", "f", "g", "n", "i", "old.id", "new.id", "COALESCE(old.id,new.id)", "CAST(old.id AS BIGINT)", "id+1"} {
			cases = append(cases, struct {
				output string
				d      referenceDiagnostic
			}{"sum(" + column + ")", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}})
		}
		for _, tc := range cases {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "UPDATE t SET id=2 RETURNING " + tc.output
				d := tc.d
				if prefix != "" {
					p, _ := strconv.Atoi(d.Position)
					d.Position = strconv.Itoa(p + 6)
				}
				prepareDiagnostic := d
				if backend.pg && tc.d.Code == "0A000" {
					prepareDiagnostic = referenceDiagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: d.Position}
					d = prepareDiagnostic
					if tc.output == "sum($1)" {
						position, _ := strconv.Atoi(d.Position)
						d = referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(position + 4)}
					}
				}
				result, err := db.Exec(q)
				if result != nil {
					t.Fatal("failed aggregate exposed result", result)
				}
				updateReturningReferenceDiagnostic(t, err, d)
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
					t.Fatal("invalid aggregate cursor")
				}
				updateReturningReferenceDiagnostic(t, err, d)
				held, err := db.Prepare(q)
				if held != nil {
					held.Close()
					t.Fatal("invalid aggregate handle")
				}
				updateReturningReferenceDiagnostic(t, err, prepareDiagnostic)
			}
		}
		for _, q := range []string{"SELECT sum(id) FROM t", "UPDATE t SET id=sum(id)", "INSERT INTO t(id) VALUES(sum(1))"} {
			result, err := db.Exec(q)
			if !backend.pg {
				if result != nil {
					t.Fatal("unsupported aggregate exposed result", result)
				}
				updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(strings.Index(q, "sum") + 1)})
			} else if strings.HasPrefix(q, "SELECT") {
				deleteReferenceCount(t, result, err, 1)
			} else {
				message := "aggregate functions are not allowed in UPDATE"
				if strings.HasPrefix(q, "INSERT") {
					message = "aggregate functions are not allowed in VALUES"
				}
				if result != nil {
					t.Fatal("failed aggregate exposed result", result)
				}
				updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "42803", Message: message, Position: strconv.Itoa(strings.Index(q, "sum") + 1)})
			}
		}
		rows, err := db.Query("SELECT id,v FROM t")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "v"}, types: []string{"INT4", "TEXT"}, rows: [][]any{{int64(1), "unchanged"}}, bag: false})
	}
}

func TestReferenceUpdateReturningCountCoexistenceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1),(NULL),(2)")
		q := "SELECT count(*),count(id),count(NULL) FROM t"
		rows, err := db.Query(q)
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"count", "count", "count"}, types: []string{"INT8", "INT8", "INT8"}, rows: [][]any{{int64(3), int64(2), int64(0)}}, bag: false})
		held, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error("statement close", err)
			}
		}()
		rows, err = held.Query()
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"count", "count", "count"}, types: []string{"INT8", "INT8", "INT8"}, rows: [][]any{{int64(3), int64(2), int64(0)}}, bag: false})
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct {
			output string
			d      referenceDiagnostic
		}{
			{"count(*)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
			{"count(id)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
			{"count(NULL)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
			{"count()", referenceDiagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function"}},
			{"count(id,id)", referenceDiagnostic{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint}},
			{"count(zzzzz)", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`}},
		} {
			for _, command := range []struct {
				sql      string
				position int
			}{{"UPDATE t SET id=2 RETURNING ", 29}, {"DELETE FROM t RETURNING ", 25}} {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + command.sql + tc.output
					d := tc.d
					position := command.position
					if tc.output == "count(zzzzz)" {
						position += 6
					}
					if prefix != "" {
						position += 6
					}
					d.Position = strconv.Itoa(position)
					result, err := db.Exec(q)
					if result != nil {
						t.Fatal("failed Exec exposed result", result)
					}
					updateReturningReferenceDiagnostic(t, err, d)
					rows, err := db.Query(q)
					if rows != nil {
						rows.Close()
						t.Fatal("invalid aggregate cursor", q)
					}
					updateReturningReferenceDiagnostic(t, err, d)
					handle, err := db.Prepare(q)
					if handle != nil {
						handle.Close()
						t.Fatal("invalid aggregate handle", q)
					}
					updateReturningReferenceDiagnostic(t, err, d)
				}
			}
		}
		rows, err = db.Query("SELECT id FROM t")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}, {nil}, {int64(2)}}, bag: true})
	}
}
