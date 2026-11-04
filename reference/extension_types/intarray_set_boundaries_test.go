package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySetBoundaries(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarrayInstall(t, p, b, c)
		for _, tc := range []struct {
			left, op, right string
			want            any
		}{
			{"{}", "&&", "{}", false}, {"{}", "&&", "{1}", false}, {"{1}", "&&", "{}", false}, {"{1}", "&&", "{2}", false},
			{"{}", "@>", "{}", true}, {"{1}", "@>", "{}", true}, {"{}", "@>", "{1}", false}, {"{1}", "@>", "{1,1}", true},
			{"{}", "<@", "{}", true}, {"{}", "<@", "{1}", true}, {"{1}", "<@", "{}", false}, {"{1,1}", "<@", "{1}", true},
			{"{}", "+", "{}", "{}"}, {"{}", "+", "{3,1,3}", "{3,1,3}"}, {"{3,1,3}", "+", "{}", "{3,1,3}"},
			{"{2,4,2}", "+", "{3,1,3,2}", "{2,4,2,3,1,3,2}"},
			{"{}", "-", "{}", "{}"}, {"{}", "-", "{1}", "{}"}, {"{3,1,3,2}", "-", "{}", "{1,2,3}"},
			{"{3,1,3,2}", "-", "{3,3}", "{1,2}"}, {"{1,1}", "-", "{1}", "{}"},
			{"{}", "|", "{}", "{}"}, {"{}", "|", "{3,1,3}", "{1,3}"}, {"{3,1,3}", "|", "{}", "{1,3}"},
			{"{}", "&", "{}", "{}"}, {"{}", "&", "{1}", "{}"}, {"{1}", "&", "{}", "{}"}, {"{1}", "&", "{2}", "{}"},
			{"{2,2,2}", "&", "{2,2}", "{2}"},
			{"{-2147483648,0,2147483647,0}", "|", "{2147483647,-2147483648}", "{-2147483648,0,2147483647}"},
			{"{2147483647,-2147483648,0}", "&", "{-2147483648,2147483647}", "{-2147483648,2147483647}"},
			{"{-1,0,-1,1}", "-", "{1}", "{-1,0}"},
		} {
			family := "_INT4"
			if _, ok := tc.want.(bool); ok {
				family = "BOOL"
			}
			query := "SELECT '" + tc.left + "'::integer[] OPERATOR(ia_provider." + tc.op + ") '" + tc.right + "'::integer[] AS v"
			intarrayBoth(t, p, b, c, intarraySetResult(b, query, family, tc.want))
		}
		for _, tc := range []struct {
			left, op string
			right    int64
			want     string
		}{
			{"{}", "+", 2, "{2}"}, {"{}", "-", 2, "{}"}, {"{}", "|", 2, "{2}"},
			{"{3,1,3,2}", "-", 9, "{3,1,3,2}"},
			{"[0:3]={3,2,1,2}", "-", 2, "[0:1]={3,1}"},
			{"[0:3]={3,2,1,2}", "-", 3, "[0:2]={2,1,2}"},
			{"[0:3]={3,2,1,2}", "-", 9, "[0:3]={3,2,1,2}"},
			{"{{3,2},{1,2}}", "-", 2, "{{3},{1}}"},
			{"{{3,2},{1,2}}", "-", 9, "{{3,2},{1,2}}"},
			{"{{{3,2},{1,2}}}", "-", 2, "{{{3}},{{1}}}"},
			{"{{{2,2},{2,2}}}", "-", 2, "{}"},
			{"{{3,1},{3,2}}", "+", 9, "{3,1,3,2,9}"},
			{"[0:1]={3,1}", "|", 2, "{1,2,3}"},
		} {
			q := "SELECT '" + tc.left + "'::integer[] OPERATOR(ia_provider." + tc.op + ") " + intarraySetInteger(tc.right) + " AS v"
			intarrayBoth(t, p, b, c, intarraySetResult(b, q, "_INT4", tc.want))
		}
		for _, tc := range []struct {
			left, op, right, want, dims string
			rank, lower, upper          any
		}{
			{"{{3,1},{3,2}}", "+", "{4}", "{3,1,3,2,4}", "[1:5]", int64(1), int64(1), int64(5)},
			{"{{3,1},{3,2}}", "|", "{}", "{1,2,3}", "[1:3]", int64(1), int64(1), int64(3)},
			{"[0:3]={3,2,1,2}", "&", "{2,3}", "{2,3}", "[1:2]", int64(1), int64(1), int64(2)},
			{"{{3,2},{1,2}}", "-", "{}", "{1,2,3}", "[1:3]", int64(1), int64(1), int64(3)},
		} {
			expr := "'" + tc.left + "'::integer[] OPERATOR(ia_provider." + tc.op + ") '" + tc.right + "'::integer[]"
			q := "SELECT v,array_dims(v) AS dims,array_ndims(v) AS rank,array_lower(v,1) AS lo,array_upper(v,1) AS hi FROM (SELECT " + expr + " AS v) s"
			intarrayBoth(t, p, b, c, intarraySelect(b, q, []string{"v", "dims", "rank", "lo", "hi"}, []string{"_INT4", "TEXT", "INT4", "INT4", "INT4"}, nil, [][]any{{tc.want, tc.dims, tc.rank, tc.lower, tc.upper}}))
		}
		for _, tc := range []struct {
			expr         string
			rank, lo, hi any
			dims         any
		}{
			{"'[0:3]={3,2,1,2}'::integer[] OPERATOR(ia_provider.-) 2", int64(1), int64(0), int64(1), "[0:1]"},
			{"'{{3,2},{1,2}}'::integer[] OPERATOR(ia_provider.-) 2", int64(2), int64(1), int64(2), "[1:2][1:1]"},
			{"'{{{3,2},{1,2}}}'::integer[] OPERATOR(ia_provider.-) 2", int64(3), int64(1), int64(2), "[1:2][1:1][1:1]"},
			{"'{2}'::integer[] OPERATOR(ia_provider.-) 2", nil, nil, nil, nil},
		} {
			q := "SELECT array_ndims(v) AS rank,array_lower(v,1) AS lo,array_upper(v,1) AS hi,array_dims(v) AS dims FROM (SELECT " + tc.expr + " AS v) s"
			intarrayBoth(t, p, b, c, intarraySelect(b, q, []string{"rank", "lo", "hi", "dims"}, []string{"INT4", "INT4", "INT4", "TEXT"}, nil, [][]any{{tc.rank, tc.lo, tc.hi, tc.dims}}))
		}
	})
}
