package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeAggregateValues(t *testing.T) {
	for _, family := range []struct{ rangeType, multiType string }{{"INT4RANGE", "INT4MULTIRANGE"}, {"INT8RANGE", "INT8MULTIRANGE"}, {"NUMRANGE", "NUMMULTIRANGE"}} {
		for _, tc := range []struct{ name, a, b, union, intersection string }{
			{"disjoint", "[1,4)", "[7,9)", "{[1,4),[7,9)}", "empty"},
			{"overlap", "[1,4)", "[3,7)", "{[1,7)}", "[3,4)"},
			{"touch", "[1,4)", "[4,7)", "{[1,7)}", "empty"},
			{"nested", "[1,7)", "[2,3)", "{[1,7)}", "[2,3)"},
			{"duplicate", "[1,7)", "[1,7)", "{[1,7)}", "[1,7)"},
			{"empty", "empty", "empty", "{}", "empty"},
			{"absorbing", "empty", "[1,4)", "{[1,4)}", "empty"},
			{"universal", "(,)", "[1,4)", "{(,)}", "[1,4)"},
		} {
			t.Run(family.rangeType+"/"+tc.name, func(t *testing.T) {
				rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k INT,v "+family.rangeType+")", 0))
					for _, reverse := range []bool{false, true} {
						a, z := tc.a, tc.b
						if reverse {
							a, z = z, a
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM r", map[bool]int64{false: 0, true: 3}[reverse]))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES(1,'%s'),(2,NULL),(3,'%s')", a, z), 3))
						rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT range_agg(v),range_intersect_agg(v),range_agg(v) FROM r", []string{"range_agg", "range_intersect_agg", "range_agg"}, []string{family.multiType, family.rangeType, family.multiType}, [][]any{{tc.union, tc.intersection, tc.union}}))
					}
				})
			})
		}
	}
}

func TestReferenceRangeAggregateMultirangeValues(t *testing.T) {
	for _, family := range []string{"INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE"} {
		for _, tc := range []struct{ name, a, b, union, intersection string }{
			{"disconnected", "{[1,4),[7,10)}", "{[2,3),[8,12)}", "{[1,4),[7,12)}", "{[2,3),[8,10)}"},
			{"disjoint", "{[1,4)}", "{[7,9)}", "{[1,4),[7,9)}", "{}"},
			{"empty", "{}", "{}", "{}", "{}"},
			{"absorbing", "{}", "{[1,4),[7,10)}", "{[1,4),[7,10)}", "{}"},
			{"duplicate", "{[1,4),[7,10)}", "{[1,4),[7,10)}", "{[1,4),[7,10)}", "{[1,4),[7,10)}"},
			{"unbounded", "{(,0),[7,)}", "{[-2,2),[9,12)}", "{(,2),[7,)}", "{[-2,0),[9,12)}"},
		} {
			t.Run(family+"/"+tc.name, func(t *testing.T) {
				rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k INT,v "+family+")", 0))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES(3,'%s'),(2,NULL),(1,'%s')", tc.b, tc.a), 3))
					for _, direction := range []string{"ASC", "DESC"} {
						q := "SELECT range_agg(v ORDER BY k " + direction + "),range_intersect_agg(v ORDER BY k " + direction + ") FROM r"
						rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, q, []string{"range_agg", "range_intersect_agg"}, []string{family, family}, [][]any{{tc.union, tc.intersection}}))
					}
				})
			})
		}
	}
}

func TestReferenceRangeAggregateNumericBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, a, b, union, intersection, reverse string }{
		{"exclusive_gap", "(1,2)", "(2,3)", "{(1,2),(2,3)}", "empty", "empty"},
		{"closed_point", "[1,2]", "[2,3]", "{[1,3]}", "[2,2]", "[2,2]"},
		{"excluded_point", "[1,2)", "[2,3]", "{[1,3]}", "empty", "empty"},
		{"exact", "[9007199254740993.1,9007199254740993.3)", "[9007199254740993.2,9007199254740993.4)", "{[9007199254740993.1,9007199254740993.4)}", "[9007199254740993.2,9007199254740993.3)", "[9007199254740993.2,9007199254740993.3)"},
		{"lower_tie", "[1.0,3.00)", "[1.000,4.0)", "{[1.000,4.0)}", "[1.0,3.00)", "[1.000,3.00)"},
		{"upper_tie", "[1.0,4.00)", "[2.000,4.000)", "{[1.0,4.000)}", "[2.000,4.00)", "[2.000,4.000)"},
		{"infinities", "[-Infinity,Infinity]", "(,)", "{(,)}", "[-Infinity,Infinity]", "[-Infinity,Infinity]"},
		{"nan", "[NaN,NaN]", "[NaN,NaN]", "{[NaN,NaN]}", "[NaN,NaN]", "[NaN,NaN]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k INT,v NUMRANGE)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES(1,'%s'),(2,'%s')", tc.a, tc.b), 2))
				for _, direction := range []string{"ASC", "DESC"} {
					intersection := tc.intersection
					if direction == "DESC" {
						intersection = tc.reverse
					}
					rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT range_agg(v ORDER BY k "+direction+"),range_intersect_agg(v ORDER BY k "+direction+") FROM r", []string{"range_agg", "range_intersect_agg"}, []string{"NUMMULTIRANGE", "NUMRANGE"}, [][]any{{tc.union, intersection}}))
				}
			})
		})
	}
}
