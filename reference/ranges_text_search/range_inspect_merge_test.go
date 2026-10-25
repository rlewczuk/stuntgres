package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func inspectHullStep(b fixture.Backend, expr, typ string, value any) fixture.Step {
	return inspectSelect(b, "SELECT "+expr+" AS hull", []string{"hull"}, []string{strings.ToUpper(typ)}, [][]any{{value}})
}
func inspectInputHull(t *testing.T, p *fixture.Pair, b fixture.Backend, inputType, resultType string, a, c, want any, unary bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_hull(a "+inputType+",b "+inputType+")", 0))
	insert := fixture.Exec("INSERT INTO inspect_hull VALUES ($1,$2)", 1, a, c)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(insert))
	expr := "range_merge(a,b)"
	if unary {
		expr = "range_merge(a)"
	}
	s := inspectHullStep(b, expr, resultType, want)
	s.Query += " FROM inspect_hull"
	inspectRunBoth(t, p, b, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE inspect_hull", 0))
}
func TestReferenceRangeInspectMerge(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, f := range inspectFamilies {
			for _, tc := range []struct{ a, c, w any }{
				{f.finite, f.finite, f.hull}, {f.finite, "empty", f.hull}, {"empty", f.finite, f.hull}, {"empty", "empty", "empty"},
				{f.finite, nil, nil}, {nil, f.finite, nil}, {"empty", nil, nil}, {nil, "empty", nil}, {nil, nil, nil},
				{f.finite, "(,)", "(,)"}, {"(,)", f.finite, "(,)"},
			} {
				inspectInputHull(t, p, b, f.name, f.name, tc.a, tc.c, tc.w, false)
			}
			var a, c, w string
			switch f.name {
			case "int4range", "int8range", "numrange":
				a, c, w = "[1,2)", "[3,4)", "[1,4)"
			case "daterange":
				a, c, w = "[2000-01-01,2000-01-02)", "[2000-01-03,2000-01-04)", "[2000-01-01,2000-01-04)"
			case "tsrange":
				a, c, w = "[2000-01-01,2000-01-02)", "[2000-01-03,2000-01-04)", `["2000-01-01 00:00:00","2000-01-04 00:00:00")`
			case "tstzrange":
				a, c, w = "[2000-01-01+00,2000-01-02+00)", "[2000-01-03+00,2000-01-04+00)", `["2000-01-01 00:00:00+00","2000-01-04 00:00:00+00")`
			}
			for _, args := range [][2]string{{a, c}, {c, a}} {
				inspectInputHull(t, p, b, f.name, f.name, args[0], args[1], w, false)
			}
			inspectInputHull(t, p, b, f.name, f.name, w, a, w, false)
			inspectInputHull(t, p, b, f.name, f.name, a, w, w, false)
			overlapA, overlapB := "[1,3)", "[2,4)"
			if f.name == "daterange" || f.name == "tsrange" || f.name == "tstzrange" {
				overlapA, overlapB = "[2000-01-01,2000-01-03)", "[2000-01-02,2000-01-04)"
			}
			inspectInputHull(t, p, b, f.name, f.name, overlapA, overlapB, w, false)
			if f.name == "numrange" || f.name == "daterange" || f.name == "tsrange" || f.name == "tstzrange" {
				inspectInputHull(t, p, b, f.name, f.name, "[-infinity,infinity]", "(,)", "(,)", false)
			}
		}
		for _, tc := range []struct{ a, c, w string }{
			{"(1,2)", "[1,3]", "[1,3]"}, {"[1,3]", "(1,2)", "[1,3]"}, {"[1,4]", "[2,3]", "[1,4]"}, {"[1,3]", "[2,4]", "[1,4]"},
			{"[1.0,2.00]", "[1.000,2.0]", "[1.000,2.0]"}, {"[1.000,2.0]", "[1.0,2.00]", "[1.0,2.00]"},
		} {
			inspectInputHull(t, p, b, "numrange", "numrange", tc.a, tc.c, tc.w, false)
		}
		for _, f := range inspectFamilies[:3] {
			name := strings.Replace(f.name, "range", "multirange", 1)
			for _, tc := range []struct{ input, w any }{
				{"{}", "empty"}, {"{empty,empty}", "empty"}, {"{[8,9),empty,[1,3),[2,4)}", "[1,9)"}, {"{(,2),[8,)}", "(,)"}, {"{[1,3)}", "[1,3)"}, {nil, nil},
			} {
				inspectInputHull(t, p, b, name, f.name, tc.input, nil, tc.w, true)
			}
		}
		inspectInputHull(t, p, b, "nummultirange", "numrange", "{(1.00,2.0),[5.000,9.0]}", nil, "(1.00,9.0]", true)
	})
}
