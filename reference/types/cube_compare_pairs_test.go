package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeComparisonPairs(t *testing.T) {
	// Signs are independently derived from REL_18_6 cube_cmp_v0, not from
	// formatted output or either backend; root's pinned18.6 run confirmed them.
	cases := []struct {
		name, left, right string
		sign              int
	}{
		{"coordinate_before_dimension", "(2)", "(1,100)", 1},
		{"all_common_lowers", "(0,5),(100,5)", "(0,6),(1,6)", -1},
		{"common_uppers", "(0,0),(2,9)", "(0,0),(3,1)", -1},
		{"common_upper_before_extra_lower", "(0),(2)", "(0,-1),(3,-1)", -1},
		{"extra_lower", "(1,-1)", "(1)", -1},
		{"dimension_tiebreak", "(1,0)", "(1)", 1},
		{"extra_upper", "(1,0),(1,2)", "(1)", 1},
		{"empty_before_zero", "()", "(0)", -1},
		{"empty_equal", "()", "()", 0},
		{"reversed_corners", "(3,1),(1,4)", "(1,4),(3,1)", 0},
		{"per_axis_orientation", "(3,1),(1,4)", "(1,1),(3,4)", 0},
		{"alternate_text", "1", "[(1),(1)]", 0},
		{"signed_zero", "(-0)", "(0)", 0},
		{"infinities", "(-Infinity)", "(Infinity)", -1},
		{"adjacent_floats", "(0.10000000000000001)", "(0.10000000000000002)", -1},
		{"hundred_equal", "(" + strings.Repeat("1,", 99) + "1)", "(" + strings.Repeat("1,", 99) + "1)", 0},
		{"hundred_last", "(" + strings.Repeat("1,", 99) + "1)", "(" + strings.Repeat("1,", 99) + "2)", -1},
		{"nan_zero", "(NaN)", "(0)", 0},
		{"nan_one", "(NaN)", "(1)", 0},
		{"nan_first_endpoint", "(NaN),(1)", "(1)", 0},
		{"nan_second_endpoint", "(1),(NaN)", "(0)", 0},
		{"cycle_a_b", "(0),(2)", "(0,1),(1,1)", 1},
		{"cycle_b_c", "(0,1),(1,1)", "(0,0),(3,0)", 1},
		{"cycle_c_a", "(0,0),(3,0)", "(0),(2)", 1},
	}
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Exec("CREATE TABLE cube_compare_pair(l cube,r cube)", 0))
		query := "SELECT " + cubeComparisonProjection("$1::cube", "$2::cube", false)
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, swap := range []bool{false, true} {
					left, right, sign := tc.left, tc.right, tc.sign
					if swap {
						left, right, sign = right, left, -sign
					}
					want := cubeComparisonRow(sign)
					for _, functions := range []bool{false, true} {
						q := "SELECT " + cubeComparisonProjection(cubeLiteral(left)+"::cube", cubeLiteral(right)+"::cube", functions)
						s := cubeComparisonStep(q, want)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeComparisonStep(query, want, left, []byte(right)))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM cube_compare_pair", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_compare_pair VALUES ($1,$2)", 1, left, right))
					q := "SELECT " + cubeComparisonProjection("l", cubeLiteral(right)+"::cube", false) + " FROM cube_compare_pair"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, cubeComparisonStep(q, want))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM cube_compare_pair", 1))
				}
			})
		}
		for _, args := range [][]any{{nil, "(0)"}, {"(0)", nil}, {nil, nil}} {
			want := []any{nil, nil, nil, nil, nil, nil, nil}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeComparisonStep(query, want, args...))
			for _, functions := range []bool{false, true} {
				q := "SELECT " + cubeComparisonProjection("$1::cube", "$2::cube", functions)
				s := cubeComparisonStep(q, want, args...)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.Select("SELECT '(3,1),(1,4)'::cube OPERATOR(public.=) '(1,1),(3,4)'::cube AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
