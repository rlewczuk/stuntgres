package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func seriesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func seriesBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func seriesRows(values ...int64) [][]any {
	var rows [][]any
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	return rows
}

func TestReferenceSeriesValues(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args, typ string
			values    []int64
		}{
			{"1,3", "INT4", []int64{1, 2, 3}},
			{"5,1,-2", "INT4", []int64{5, 3, 1}},
			{"1,6,2", "INT4", []int64{1, 3, 5}},
			{"3,3,2", "INT4", []int64{3}},
			{"3,3,-2", "INT4", []int64{3}},
			{"3,1", "INT4", nil},
			{"1,3,-1", "INT4", nil},
			{"NULL,3", "INT4", nil},
			{"1,NULL", "INT4", nil},
			{"1,3,NULL", "INT4", nil},
			{"NULL,3,0", "INT4", nil},
			{"1,NULL,0", "INT4", nil},
			{"1::bigint,3::bigint", "INT8", []int64{1, 2, 3}},
			{"5::bigint,1::bigint,-2::bigint", "INT8", []int64{5, 3, 1}},
			{"NULL::bigint,3::bigint", "INT8", nil},
		} {
			t.Run(tc.args, func(t *testing.T) {
				s := fixture.Select("SELECT generate_series("+tc.args+")", []string{"generate_series"}, []string{tc.typ}, seriesRows(tc.values...))
				s.Bag = true
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceSeriesOverflow(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args, typ string
			values    []int64
		}{
			{"2147483646,2147483647,2", "INT4", []int64{2147483646}},
			{"2147483647,2147483647,1", "INT4", []int64{2147483647}},
			{"-2147483647,-2147483648,-2", "INT4", []int64{-2147483647}},
			{"-2147483648,-2147483648,-1", "INT4", []int64{-2147483648}},
			{"0,-2147483648,-2147483648", "INT4", []int64{0, -2147483648}},
			{"9223372036854775806::bigint,9223372036854775807::bigint,2::bigint", "INT8", []int64{9223372036854775806}},
			{"9223372036854775807::bigint,9223372036854775807::bigint,1::bigint", "INT8", []int64{9223372036854775807}},
			{"-9223372036854775807::bigint,(-9223372036854775808)::bigint,-2::bigint", "INT8", []int64{-9223372036854775807}},
			{"(-9223372036854775808)::bigint,(-9223372036854775808)::bigint,-1::bigint", "INT8", []int64{-9223372036854775808}},
			{"0::bigint,(-9223372036854775808)::bigint,(-9223372036854775808)::bigint", "INT8", []int64{0, -9223372036854775808}},
			{"-1::bigint,9223372036854775807::bigint,9223372036854775807::bigint", "INT8", []int64{-1, 9223372036854775806}},
		} {
			t.Run(tc.args, func(t *testing.T) {
				s := fixture.Select("SELECT generate_series("+tc.args+") AS v", []string{"v"}, []string{tc.typ}, seriesRows(tc.values...))
				s.Bag = true
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceSeriesMetadata(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args, typ string
			rows      [][]any
		}{
			{"1,3", "INT4", seriesRows(1, 2, 3)},
			{"NULL,3", "INT4", nil},
			{"1::bigint,3::bigint", "INT8", seriesRows(1, 2, 3)},
			{"NULL::bigint,3::bigint", "INT8", nil},
		} {
			t.Run(tc.args, func(t *testing.T) {
				s := fixture.Select("SELECT generate_series("+tc.args+") AS v ORDER BY v", []string{"v"}, []string{tc.typ}, tc.rows)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(cols) != 1 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != tc.typ {
						t.Fatalf("metadata: %v", cols)
					}
				}
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}
