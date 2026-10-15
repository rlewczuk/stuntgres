package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeOperatorComparisons(t *testing.T) {
	for _, f := range operatorFamilies {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE op(k integer,a "+f.name+",b "+f.name+")", 0))
				a, c, d := f.span(f.one, f.three), f.span(f.two, f.seven), f.span(f.one, f.seven)
				run(fixture.Exec("INSERT INTO op VALUES (1,"+rangeLiteral(a)+","+rangeLiteral(c)+"),(2,"+rangeLiteral(c)+","+rangeLiteral(a)+"),(3,"+rangeLiteral(a)+","+rangeLiteral(a)+"),(4,'empty',"+rangeLiteral(a)+"),(5,"+rangeLiteral(a)+","+rangeLiteral(d)+"),(6,'empty','empty'),(7,NULL,"+rangeLiteral(a)+"),(8,"+rangeLiteral(a)+",NULL)", 8))
				for _, tc := range []struct {
					op         string
					lt, gt, eq bool
				}{{"=", false, false, true}, {"<>", true, true, false}, {"!=", true, true, false}, {"<", true, false, false}, {"<=", true, false, true}, {">", false, true, false}, {">=", false, true, true}} {
					operatorBool(t, p, b, "a "+tc.op+" b", "op ORDER BY k", [][]any{{tc.lt}, {tc.gt}, {tc.eq}, {tc.lt}, {tc.lt}, {tc.eq}, {nil}, {nil}})
				}
				operatorBool(t, p, b, "a < '["+f.one+","+f.seven+")'", "op WHERE k=1", [][]any{{true}})
				operatorBool(t, p, b, "'["+f.one+","+f.seven+")' > a", "op WHERE k=1", [][]any{{true}})
				operatorBool(t, p, b, "a = b", "op WHERE k=99", [][]any{})
			})
		})
	}
	for _, f := range operatorFamilies[:3] {
		t.Run(f.multi(), func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE op(k integer,a "+f.multi()+",b "+f.multi()+")", 0))
				run(fixture.Exec("INSERT INTO op VALUES (1,'{[1,2),[7,9)}','{[1,3),[7,9)}'),(2,'{[1,3),[7,9)}','{[1,2),[7,9)}'),(3,'{[1,2)}','{[1,2),[7,9)}'),(4,'{}','{[1,2)}'),(5,'{}','{}'),(6,NULL,'{}')", 6))
				for _, tc := range []struct {
					op         string
					lt, gt, eq bool
				}{{"=", false, false, true}, {"<>", true, true, false}, {"!=", true, true, false}, {"<", true, false, false}, {"<=", true, false, true}, {">", false, true, false}, {">=", false, true, true}} {
					operatorBool(t, p, b, "a "+tc.op+" b", "op ORDER BY k", [][]any{{tc.lt}, {tc.gt}, {tc.lt}, {tc.lt}, {tc.eq}, {nil}})
				}
			})
		})
	}
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE ties(a numrange,b numrange,c tsrange,d tsrange,e tstzrange,f tstzrange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ties VALUES ('[1.0,2.00]','[1.000,2.0]','[2000-01-01,2000-01-02)','(2000-01-01,2000-01-02)','[2000-01-01 00:00+00,2000-01-02 00:00+00)','[1999-12-31 19:00-05,2000-01-01 19:00-05)')", 1))
		operatorBool(t, p, b, "a = b", "ties", [][]any{{true}})
		operatorBool(t, p, b, "c < d", "ties", [][]any{{true}})
		operatorBool(t, p, b, "e = f", "ties", [][]any{{true}})
	})
}
