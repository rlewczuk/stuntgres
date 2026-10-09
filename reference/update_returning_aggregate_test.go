package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceUpdateReturningAggregateDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,v TEXT,s SMALLINT,b BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,i INTERVAL)", 0))
			run(referenceExec("INSERT INTO t(id,v) VALUES(1,'unchanged')", 1))
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			cases := []struct {
				output string
				d      referenceDiagnostic
			}{
				{"sum(zzzzz)", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "33"}},
				{"sum(v)", referenceDiagnostic{Code: "42883", Message: "function sum(text) does not exist", Hint: hint, Position: "29"}},
				{"public.sum(id)", referenceDiagnostic{Code: "42883", Message: "function public.sum(integer) does not exist", Hint: hint, Position: "29"}},
				{"pg_catalog.sum(id)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
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
						position, _ := strconv.Atoi(d.Position)
						d.Position = strconv.Itoa(position + 6)
					}
					run(referenceImplicitError(q, d))
					queryError := referenceImplicitError(q, d)
					queryError.selectRows = true
					run(queryError)
					run(referencePrepared(referenceImplicitError(q, d)))
				}
			}
			run(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}))
		})
	}
}
