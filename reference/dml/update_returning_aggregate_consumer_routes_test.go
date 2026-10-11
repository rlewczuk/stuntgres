package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningAggregateDiagnosticsConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,v TEXT,s SMALLINT,b BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,i INTERVAL)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t(id,v) VALUES(1,'unchanged')")
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		cases := []struct {
			output string
			d      fixture.Diagnostic
		}{
			{"sum(zzzzz)", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "33"}},
			{"sum(v)", fixture.Diagnostic{Code: "42883", Message: "function sum(text) does not exist", Hint: hint, Position: "29"}},
			{"public.sum(id)", fixture.Diagnostic{Code: "42883", Message: "function public.sum(integer) does not exist", Hint: hint, Position: "29"}},
			{"pg_catalog.sum(id)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
			{"sum('7')", fixture.Diagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "29"}},
			{"sum(NULL)", fixture.Diagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "29"}},
			{"sum($1)", fixture.Diagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "29"}},
		}
		for _, column := range []string{"id", "s", "b", "f", "g", "n", "i", "old.id", "new.id", "COALESCE(old.id,new.id)", "CAST(old.id AS BIGINT)", "id+1"} {
			cases = append(cases, struct {
				output string
				d      fixture.Diagnostic
			}{"sum(" + column + ")", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}})
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
				if backend.PG && tc.output == "sum($1)" {
					position, _ := strconv.Atoi(d.Position)
					d = fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(position + 4)}
				}
				result, err := db.Exec(q)
				if result != nil {
					t.Fatal("failed aggregate exposed result", result)
				}
				fixture.UpdateReturningReferenceDiagnostic(t, err, d)
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
					t.Fatal("invalid aggregate cursor")
				}
				fixture.UpdateReturningReferenceDiagnostic(t, err, d)
				held, err := db.Prepare(q)
				if held != nil {
					held.Close()
					t.Fatal("invalid aggregate handle")
				}
				fixture.UpdateReturningReferenceDiagnostic(t, err, prepareDiagnostic)
			}
		}
		for _, q := range []string{"SELECT sum(id) FROM t", "UPDATE t SET id=sum(id)", "INSERT INTO t(id) VALUES(sum(1))"} {
			result, err := db.Exec(q)
			if strings.HasPrefix(q, "SELECT") {
				fixture.DeleteReferenceCount(t, result, err, 1)
			} else {
				message := "aggregate functions are not allowed in UPDATE"
				if strings.HasPrefix(q, "INSERT") {
					message = "aggregate functions are not allowed in VALUES"
				}
				if result != nil {
					t.Fatal("failed aggregate exposed result", result)
				}
				fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42803", Message: message, Position: strconv.Itoa(strings.Index(q, "sum") + 1)})
			}
		}
		rows, err := db.Query("SELECT id,v FROM t")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "v"}, Types: []string{"INT4", "TEXT"}, Rows: [][]any{{int64(1), "unchanged"}}, Bag: false})
	}
}

func TestReferenceUpdateReturningCountCoexistenceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1),(NULL),(2)")
		q := "SELECT count(*),count(id),count(NULL) FROM t"
		rows, err := db.Query(q)
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"count", "count", "count"}, Types: []string{"INT8", "INT8", "INT8"}, Rows: [][]any{{int64(3), int64(2), int64(0)}}, Bag: false})
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
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"count", "count", "count"}, Types: []string{"INT8", "INT8", "INT8"}, Rows: [][]any{{int64(3), int64(2), int64(0)}}, Bag: false})
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct {
			output string
			d      fixture.Diagnostic
		}{
			{"count(*)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
			{"count(id)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
			{"count(NULL)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
			{"count()", fixture.Diagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function"}},
			{"count(id,id)", fixture.Diagnostic{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint}},
			{"count(zzzzz)", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`}},
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
					fixture.UpdateReturningReferenceDiagnostic(t, err, d)
					rows, err := db.Query(q)
					if rows != nil {
						rows.Close()
						t.Fatal("invalid aggregate cursor", q)
					}
					fixture.UpdateReturningReferenceDiagnostic(t, err, d)
					handle, err := db.Prepare(q)
					if handle != nil {
						handle.Close()
						t.Fatal("invalid aggregate handle", q)
					}
					fixture.UpdateReturningReferenceDiagnostic(t, err, d)
				}
			}
		}
		rows, err = db.Query("SELECT id FROM t")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(1)}, {nil}, {int64(2)}}, Bag: true})
	}
}
