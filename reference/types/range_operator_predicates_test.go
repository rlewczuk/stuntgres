package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeOperatorPredicates(t *testing.T) {
	operatorEndpointWitnesses(t)
	for _, f := range operatorFamilies {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE op(k integer,a "+f.name+",b "+f.name+",e "+f.element+")", 0))
				a, c, d := f.span(f.one, f.three), f.span(f.two, f.seven), f.span(f.three, f.seven)
				run(fixture.Exec("INSERT INTO op VALUES (1,"+rangeLiteral(a)+","+rangeLiteral(c)+","+rangeLiteral(f.two)+"),(2,"+rangeLiteral(a)+","+rangeLiteral(d)+","+rangeLiteral(f.three)+"),(3,"+rangeLiteral(a)+","+rangeLiteral(a)+","+rangeLiteral(f.one)+"),(4,'empty','empty',"+rangeLiteral(f.one)+"),(5,"+rangeLiteral(a)+",'empty',NULL),(6,'empty',"+rangeLiteral(a)+",NULL),(7,NULL,"+rangeLiteral(a)+",NULL),(8,"+rangeLiteral(a)+",NULL,NULL),(9,'(,)',"+rangeLiteral(a)+","+rangeLiteral(f.nine)+")", 9))
				for _, tc := range []struct {
					expr string
					v    []any
				}{
					{"a @> b", []any{false, false, true, true, true, false, nil, nil, true}},
					{"a <@ b", []any{false, false, true, true, false, true, nil, nil, false}},
					{"a && b", []any{true, false, true, false, false, false, nil, nil, true}},
					{"a << b", []any{false, true, false, false, false, false, nil, nil, false}},
					{"a >> b", []any{false, false, false, false, false, false, nil, nil, false}},
					{"a &< b", []any{true, true, true, false, false, false, nil, nil, false}},
					{"a &> b", []any{false, false, true, false, false, false, nil, nil, false}},
					{"a -|- b", []any{false, true, false, false, false, false, nil, nil, false}},
					{"a @> e", []any{true, false, true, false, nil, nil, nil, nil, true}},
					{"e <@ a", []any{true, false, true, false, nil, nil, nil, nil, true}},
				} {
					rows := make([][]any, len(tc.v))
					for i, v := range tc.v {
						rows[i] = []any{v}
					}
					operatorBool(t, p, b, tc.expr, "op ORDER BY k", rows)
				}
				// Reverse directions provide actual true strict-right witnesses.
				operatorBool(t, p, b, "b >> a", "op WHERE k=2", [][]any{{true}})
				operatorBool(t, p, b, "b &> a", "op WHERE k=1", [][]any{{true}})
			})
		})
	}
}

func TestReferenceMultirangeOperatorPredicates(t *testing.T) {
	for _, f := range operatorFamilies[:3] {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				operatorEmptyWitnesses(t, p, b, f)
				run(fixture.Exec("CREATE TABLE op(k integer,r "+f.name+",s "+f.name+",m "+f.multi()+",n "+f.multi()+",e "+f.element+")", 0))
				run(fixture.Exec("INSERT INTO op VALUES (1,'[2,8)','[3,7)','{[1,3),[7,9)}','{[2,4),[8,10)}',2),(2,'[9,10)','[1,3)','{[1,3),[7,9)}','{[9,10)}',9),(3,'empty','empty','{}','{}',1),(4,'[1,3)','empty','{[1,3)}','{}',1),(5,'empty','[1,3)','{}','{[1,3)}',1),(6,NULL,NULL,NULL,NULL,NULL)", 6))
				for _, tc := range []struct {
					expr string
					v    []any
				}{
					{"m @> n", []any{false, false, true, true, false, nil}}, {"m <@ n", []any{false, false, true, false, true, nil}},
					{"m @> r", []any{false, false, true, true, true, nil}}, {"r <@ m", []any{false, false, true, true, true, nil}},
					{"r @> m", []any{false, false, true, true, true, nil}}, {"m <@ r", []any{false, false, true, true, true, nil}},
					{"m @> e", []any{true, false, false, true, false, nil}}, {"e <@ m", []any{true, false, false, true, false, nil}},
					{"m && n", []any{true, false, false, false, false, nil}}, {"m && r", []any{true, false, false, true, false, nil}}, {"r && m", []any{true, false, false, true, false, nil}},
					{"m << n", []any{false, true, false, false, false, nil}}, {"m << r", []any{false, true, false, false, false, nil}}, {"r << m", []any{false, false, false, false, false, nil}},
					{"m >> n", []any{false, false, false, false, false, nil}}, {"m >> r", []any{false, false, false, false, false, nil}}, {"r >> m", []any{false, true, false, false, false, nil}},
					{"m &< n", []any{true, true, false, false, false, nil}}, {"m &< r", []any{false, true, false, true, false, nil}}, {"r &< m", []any{true, false, false, true, false, nil}},
					{"m &> n", []any{false, false, false, false, false, nil}}, {"m &> r", []any{false, false, false, true, false, nil}}, {"r &> m", []any{true, true, false, true, false, nil}},
					{"m -|- n", []any{false, true, false, false, false, nil}}, {"m -|- r", []any{false, true, false, false, false, nil}}, {"r -|- m", []any{false, true, false, false, false, nil}},
				} {
					rows := make([][]any, len(tc.v))
					for i, v := range tc.v {
						rows[i] = []any{v}
					}
					operatorBool(t, p, b, tc.expr, "op ORDER BY k", rows)
				}
				operatorBool(t, p, b, "m -|- s", "op WHERE k=1", [][]any{{false}})
				// Cross containment overloads in the remaining spelled directions.
				operatorBool(t, p, b, "n <@ m", "op WHERE k=4", [][]any{{true}})
				operatorBool(t, p, b, "r @> n", "op WHERE k=4", [][]any{{true}})
				operatorBool(t, p, b, "n <@ r", "op WHERE k=4", [][]any{{true}})
				operatorBool(t, p, b, "s <@ m", "op WHERE k=2", [][]any{{true}})
				run(fixture.Exec("CREATE TABLE directions(r "+f.name+",m "+f.multi()+",n "+f.multi()+")", 0))
				run(fixture.Exec("INSERT INTO directions VALUES ('[1,3)','{[7,9)}','{[1,3)}')", 1))
				for _, expression := range []string{"r << m", "m >> n", "m >> r", "m &> n"} {
					operatorBool(t, p, b, expression, "directions", [][]any{{true}})
				}
				operatorBool(t, p, b, "m &< n", "directions", [][]any{{false}})
				operatorBool(t, p, b, "r &> m", "directions", [][]any{{false}})
				run(fixture.Exec("CREATE TABLE proper_containment(m "+f.multi()+",n "+f.multi()+")", 0))
				run(fixture.Exec("INSERT INTO proper_containment VALUES ('{[1,3),[7,9)}','{[1,2)}')", 1))
				operatorBool(t, p, b, "n <@ m", "proper_containment", [][]any{{true}})
			})
		})
	}
}
