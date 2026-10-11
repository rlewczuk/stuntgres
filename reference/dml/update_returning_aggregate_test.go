package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningAggregateDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,v TEXT,s SMALLINT,b BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,i INTERVAL)", 0))
			run(fixture.Exec("INSERT INTO t(id,v) VALUES(1,'unchanged')", 1))
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			cases := []struct {
				output string
				d      fixture.Diagnostic
			}{
				{"sum(zzzzz)", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "33"}},
				{"sum(v)", fixture.Diagnostic{Code: "42883", Message: "function sum(text) does not exist", Hint: hint, Position: "29"}},
				{"public.sum(id)", fixture.Diagnostic{Code: "42883", Message: "function public.sum(integer) does not exist", Hint: hint, Position: "29"}},
				{"pg_catalog.sum(id)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
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
						position, _ := strconv.Atoi(d.Position)
						d.Position = strconv.Itoa(position + 6)
					}
					run(fixture.ImplicitError(q, d))
					queryError := fixture.ImplicitError(q, d)
					queryError.SelectRows = true
					run(queryError)
					run(fixture.Prepared(fixture.ImplicitError(q, d)))
				}
			}
			run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}))
		})
	}
}
