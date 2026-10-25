package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Unequal operands independently reject a constant-zero comparison provider.
func TestReferenceMultirangeComparisons(t *testing.T) {
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
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE vacant(a "+operand+",b "+operand+")", 0))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE nulls(a "+operand+",b "+operand+")", 0))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO nulls VALUES (NULL,NULL)", 1))
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
							s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.ascending}, {tc.descending}, {tc.equal}, {nil}, {nil}})
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						}
						for _, route := range []struct {
							left, right, relation string
							rows                  [][]any
							args                  []any
						}{
							{"a", "'{[1,2)}'", "r ORDER BY k", [][]any{{tc.equal}, {tc.descending}, {tc.equal}, {nil}, {tc.equal}}, nil},
							{"'{[1,2)}'", "a", "r ORDER BY k", [][]any{{tc.equal}, {tc.ascending}, {tc.equal}, {nil}, {tc.equal}}, nil},
							{"a", "$1", "r ORDER BY k", [][]any{{tc.equal}, {tc.descending}, {tc.equal}, {nil}, {tc.equal}}, []any{"{[1,2)}"}},
							{"$1", "a", "r ORDER BY k", [][]any{{tc.equal}, {tc.ascending}, {tc.equal}, {nil}, {tc.equal}}, []any{[]byte("{[1,2)}")}},
							{"a", "$1", "r ORDER BY k", [][]any{{nil}, {nil}, {nil}, {nil}, {nil}}, []any{nil}},
							{"$1", "a", "r ORDER BY k", [][]any{{nil}, {nil}, {nil}, {nil}, {nil}}, []any{[]byte(nil)}},
							{"a", "b", "vacant", [][]any{}, nil}, {"a", "b", "nulls", [][]any{{nil}}, nil},
							{"a", "NULL", "r ORDER BY k", [][]any{{nil}, {nil}, {nil}, {nil}, {nil}}, nil},
							{"NULL", "a", "r ORDER BY k", [][]any{{nil}, {nil}, {nil}, {nil}, {nil}}, nil},
						} {
							operatorBool(t, p, backend, route.left+" "+tc.op+" "+route.right, route.relation, route.rows, route.args...)
						}
					}
					for _, prepared := range []bool{false, true} {
						stored.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, stored)
					}
					operatorBoth(t, p, backend, fixture.Select("SELECT k FROM r WHERE a = b ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
				}
			})
		}
	}
}
