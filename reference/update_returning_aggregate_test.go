package reference_test

import (
	"strconv"
	"strings"
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
			for _, output := range []string{"sum('7')", "sum(NULL)", "sum($1)"} {
				for _, prefix := range []string{"", "/*é*/ "} {
					want := referenceDiagnostic{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: referencePosition(29)}
					if backend.pg {
						want = referenceDiagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: referencePosition(29)}
					}
					if prefix != "" {
						want.Position = referencePosition(35)
					}
					for _, lane := range []string{"exec", "query", "prepare"} {
						diagnostic := want
						if backend.pg && output == "sum($1)" && lane != "prepare" {
							position := 33
							if prefix != "" {
								position += 6
							}
							diagnostic = referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: referencePosition(position)}
						}
						s := referenceImplicitError(prefix+"UPDATE t SET id=2 RETURNING "+output, diagnostic)
						s.selectRows, s.prepared = lane == "query", lane == "prepare"
						run(s)
					}
				}
			}
			for _, q := range []string{"SELECT sum(id) FROM t", "UPDATE t SET id=sum(id)", "INSERT INTO t(id) VALUES(sum(1))"} {
				if backend.pg {
					if q == "SELECT sum(id) FROM t" {
						run(referenceSelect(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
						continue
					}
					run(referenceError(q, "42803"))
				} else {
					run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "functions are unsupported", Position: referencePosition(strings.Index(q, "sum") + 1)}))
				}
			}
			run(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}))
		})
	}
}
