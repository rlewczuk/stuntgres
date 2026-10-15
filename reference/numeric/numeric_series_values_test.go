package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func numericSeriesRows(values ...string) [][]any {
	var rows [][]any
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	return rows
}

func TestReferenceNumericSeriesValues(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args, order string
			values      []string
		}{
			{"0.1,0.3,0.1", "ASC", []string{"0.1", "0.2", "0.3"}},
			{"0.3,0.1,-0.1", "DESC", []string{"0.3", "0.2", "0.1"}},
			{"0.1,0.6,0.2", "ASC", []string{"0.1", "0.3", "0.5"}},
			{"1.1,4,1.3", "ASC", []string{"1.1", "2.4", "3.7"}},
			{"0.10,2.10", "ASC", []string{"0.10", "1.10", "2.10"}},
			{"1,3.000,1.00", "ASC", []string{"1", "2.00", "3.00"}},
			{"1.0,3.000,1", "ASC", []string{"1.0", "2.0", "3.0"}},
			{"0.1,0.3,0.10", "ASC", []string{"0.1", "0.20", "0.30"}},
			{"-0.10,0.10,0.1", "ASC", []string{"-0.10", "0.00", "0.10"}},
			{"9007199254740993.1,9007199254740993.3,0.1", "ASC", []string{"9007199254740993.1", "9007199254740993.2", "9007199254740993.3"}},
			{"9223372036854775808,9223372036854775810,1", "ASC", []string{"9223372036854775808", "9223372036854775809", "9223372036854775810"}},
			{"0.1,0.1,1", "ASC", []string{"0.1"}},
			{"0.1,0.1,-1", "ASC", []string{"0.1"}},
			{"0.3,0.1,0.1", "ASC", nil},
			{"0.1,0.3,-0.1", "ASC", nil},
			{"NULL::numeric,1,0", "ASC", nil},
			{"1,NULL::numeric,0", "ASC", nil},
			{"1,2,NULL::numeric", "ASC", nil},
			{"NULL::numeric,'NaN'::numeric,'Infinity'::numeric", "ASC", nil},
		} {
			t.Run(tc.args, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select("SELECT generate_series("+tc.args+") AS v ORDER BY v "+tc.order, []string{"v"}, []string{"NUMERIC"}, numericSeriesRows(tc.values...)))
			})
		}
	})
}

func TestReferenceNumericSeriesBoundaries(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, tc := range []struct{ args, suffix, want string }{
			{"9e131071,9e131071,9e131071", "", "9" + strings.Repeat("0", 131071)},
			{"9e131071,9.5e131071,9e131071", "", "9" + strings.Repeat("0", 131071)},
			{"-9e131071,-9e131071,-9e131071", "", "-9" + strings.Repeat("0", 131071)},
			{"-9e131071,-9.5e131071,-9e131071", "", "-9" + strings.Repeat("0", 131071)},
			{"9e131071,9.5e131071,9e131071", " LIMIT 1", "9" + strings.Repeat("0", 131071)},
		} {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				q := "SELECT generate_series(" + tc.args + ") AS v" + tc.suffix
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					var err error
					if prepared {
						st, e := c.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						defer st.Close()
						rows, err = st.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer rows.Close()
						if !rows.Next() {
							t.Fatalf("missing extreme row: %v", rows.Err())
						}
						var got string
						if err := rows.Scan(&got); err != nil {
							t.Fatal(err)
						}
						if got != tc.want {
							offset := 0
							for offset < len(got) && offset < len(tc.want) && got[offset] == tc.want[offset] {
								offset++
							}
							t.Fatalf("extreme value length=%d want=%d first difference=%d", len(got), len(tc.want), offset)
						}
						if rows.Next() {
							t.Fatal("extra extreme row")
						}
						if err := rows.Err(); err != nil {
							t.Fatal(err)
						}
					}()
				}
			})
		}
		seriesBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM generate_series(9e131071,9.5e131071,9e131071) s(v)", []string{"n"}, []string{"INT8"}, seriesRows(1)))
		fine := "0." + strings.Repeat("0", 16382)
		seriesBoth(t, p, b, c, fixture.Select("SELECT generate_series(1e-16383,3e-16383,1e-16383) AS v ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows(fine+"1", fine+"2", fine+"3")))
	})
}

func TestReferenceNumericSeriesDemand(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT generate_series(0.1,1e100,0.1) AS v LIMIT 3", numericSeriesRows("0.1", "0.2", "0.3")},
			{"SELECT generate_series(0.1,0.3,0) AS v LIMIT 0", nil},
			{"SELECT generate_series(0.1,0.3,'NaN'::numeric) AS v LIMIT 0", nil},
			{"SELECT generate_series(0.1,0.3,0) AS v WHERE false", nil},
			{"SELECT generate_series(0.1,0.3,CASE WHEN id=1 THEN 0.1 ELSE 0 END) AS v FROM (VALUES(1),(2)) s(id) ORDER BY id LIMIT 1", numericSeriesRows("0.1")},
		} {
			t.Run(tc.q, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"NUMERIC"}, tc.rows))
			})
		}
		q := "SELECT generate_series(0.1,0.3,0) AS v ORDER BY v LIMIT 1"
		s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""))
		s.SelectRows, s.ErrorStage = true, "execute"
		seriesBoth(t, p, b, c, s)
	})
}
