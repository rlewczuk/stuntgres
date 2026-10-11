package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeAggregateDiscreteBoundaries(t *testing.T) {
	for _, f := range []struct{ r, m, minimum, maximum string }{
		{"INT4RANGE", "INT4MULTIRANGE", "-2147483648", "2147483647"},
		{"INT8RANGE", "INT8MULTIRANGE", "-9223372036854775808", "9223372036854775807"},
	} {
		for _, tc := range []struct{ name, a, z, u, i string }{
			{"closed_adjacency", "[1,4]", "[5,7)", "{[1,7)}", "empty"},
			{"singleton", "[1,4)", "[1,4)", "{[1,4)}", "[1,4)"},
			{"extrema", "[" + f.minimum + ",0)", "[0," + f.maximum + ")", "{[" + f.minimum + "," + f.maximum + ")}", "empty"},
		} {
			t.Run(f.r+"/"+tc.name, func(t *testing.T) {
				rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v "+f.r+")", 0))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES('%s'),('%s')", tc.a, tc.z), 2))
					rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT range_agg(v),range_intersect_agg(v) FROM r", []string{"range_agg", "range_intersect_agg"}, []string{f.m, f.r}, [][]any{{tc.u, tc.i}}))
				})
			})
		}
	}
}
