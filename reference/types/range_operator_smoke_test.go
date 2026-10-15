package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeOperatorFeatureSmoke(t *testing.T) {
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for i, f := range operatorFamilies {
			name := fmt.Sprintf("smoke_%d", i)
			run(fixture.Exec("CREATE TABLE "+name+"(a "+f.name+",b "+f.name+")", 0))
			run(fixture.Exec("INSERT INTO "+name+" VALUES ("+rangeLiteral(f.span(f.one, f.three))+","+rangeLiteral(f.span(f.three, f.seven))+")", 1))
			for _, tc := range []struct {
				expr string
				v    bool
			}{{"a @> a", true}, {"a && b", false}, {"a << b", true}, {"a -|- b", true}, {"a < b", true}} {
				operatorBool(t, p, b, tc.expr, name, [][]any{{tc.v}})
			}
			operatorValue(t, p, b, "a + b", name, f.name, [][]any{{operatorRangeOutput(f, f.span(f.one, f.seven))}})
			q := "SELECT a @> $1 AS v FROM " + name
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, tc := range []struct{ arg, v any }{{f.span(f.one, f.two), true}, {[]byte(f.span(f.one, f.two)), true}, {nil, nil}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.v}}, tc.arg))
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if i < 3 {
				operatorBool(t, p, b, "multirange(a) @> a", name, [][]any{{true}})
				operatorValue(t, p, b, "multirange(a) + multirange(b)", name, f.multi(), [][]any{{"{[1,7)}"}})
				operatorValue(t, p, b, "multirange(a) - multirange(b)", name, f.multi(), [][]any{{"{[1,3)}"}})
			}
		}
		run(fixture.Exec("CREATE TABLE smoke_fail(a int4range,b int4range,c int4range)", 0))
		run(fixture.Exec("INSERT INTO smoke_fail VALUES ('[1,9)','[3,7)','[11,13)')", 1))
		for _, tc := range []struct{ expr, message string }{{"a - b", "result of range difference would not be contiguous"}, {"a + c", "result of range union would not be contiguous"}} {
			s := fixture.ErrorDiagnostic("SELECT "+tc.expr+" FROM smoke_fail", fixture.ControlsReferenceDiagnostic("22000", tc.message))
			s.SelectRows = true
			s.ErrorStage = "execute"
			operatorBoth(t, p, b, s)
		}
		operatorValue(t, p, b, "multirange(a) - multirange(b)", "smoke_fail", "int4multirange", [][]any{{"{[1,3),[7,9)}"}})
		operatorValue(t, p, b, "multirange(b) + multirange(c)", "smoke_fail", "int4multirange", [][]any{{"{[3,7),[11,13)}"}})
		q := "SELECT a + multirange(b) FROM smoke_fail"
		operatorDiagnostic(t, p, b, q, operatorMissing(q, "int4range", "+", "int4multirange"), "prepare")
		checks, nullable := operatorStoredRecipes(t, p, b)
		operatorControlCycle(t, p, b, checks, nullable, nil)
	})
}
