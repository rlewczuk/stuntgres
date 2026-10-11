package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL supplies comparison providers; native deliberately admits storage
// only. Unequal operands prevent a constant-zero comparison from passing.
func TestReferenceMultirangeComparisonBoundary(t *testing.T) {
	for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
		for _, shape := range []string{"builtin", "domain", "nested_domain"} {
			t.Run(typ+"/"+shape, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					operand := typ
					if shape != "builtin" {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE DOMAIN public.span AS pg_catalog."+typ, 0))
						operand = "span"
						if shape == "nested_domain" {
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE DOMAIN public.chain AS public.span", 0))
							operand = "chain"
						}
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,a "+operand+",b "+operand+")", 0))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'{[1,2)}','{[8,9)}'),(2,'{[8,9)}','{[1,2)}'),(3,'{[1,2)}','{[1,2)}'),(4,NULL,'{[1,2)}'),(5,'{[1,2)}',NULL)", 5))
					stored := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", strings.ToUpper(typ), strings.ToUpper(typ)}, [][]any{{int64(1), "{[1,2)}", "{[8,9)}"}, {int64(2), "{[8,9)}", "{[1,2)}"}, {int64(3), "{[1,2)}", "{[1,2)}"}, {int64(4), nil, "{[1,2)}"}, {int64(5), "{[1,2)}", nil}}, []bool{false, true, true})
					for _, tc := range []struct {
						op                           string
						ascending, descending, equal bool
					}{
						{"=", false, false, true}, {"<>", true, true, false},
						{"<", true, false, false}, {"<=", true, false, true},
						{">", false, true, false}, {">=", false, true, true},
					} {
						q := "SELECT a " + tc.op + " b AS v FROM r ORDER BY k"
						for _, prepared := range []bool{false, true} {
							if backend.PG {
								s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.ascending}, {tc.descending}, {tc.equal}, {nil}, {nil}})
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							} else {
								want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist: " + operand + " " + tc.op + " " + operand, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}
								s := fixture.ErrorDiagnostic(q, want)
								s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
								if prepared {
									s.ErrorStage = "prepare"
								}
								fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							}
						}
					}
					for _, prepared := range []bool{false, true} {
						stored.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, stored)
					}
				}
			})
		}
	}
}
