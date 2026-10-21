package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestampSeriesInfinity(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, value := range []string{"infinity", "-infinity"} {
			q := "SELECT generate_series(TIMESTAMP '" + value + "',TIMESTAMP '" + value + "',INTERVAL '1 day') AS v LIMIT 3"
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
					meta, e := rows.ColumnTypes()
					if e != nil {
						t.Fatal(e)
					}
					fixture.TemporalMetadata(t, b.PG, meta)
					if len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMP" {
						t.Fatal(meta)
					}
					n := 0
					for rows.Next() {
						var raw any
						if e := rows.Scan(&raw); e != nil {
							t.Fatal(e)
						}
						if fixture.Normalize(raw) != value {
							t.Fatalf("raw infinity=%T(%v), want %s", raw, raw, value)
						}
						n++
					}
					if rows.Err() != nil || n != 3 {
						t.Fatalf("count=%d err=%v", n, rows.Err())
					}
				}()
			}
		}
	})
}
