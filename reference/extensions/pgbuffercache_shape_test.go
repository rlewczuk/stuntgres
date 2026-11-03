package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheShape(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		capacity := bcCapacity(t, p, c)
		renamed := []string{"id", "node", "space", "database", "fork", "block", "dirty", "usage", "pins"}
		for _, tc := range []struct {
			name, from   string
			names, types []string
			view         bool
		}{
			{"view", "bc_part.pg_buffercache", bcNames, bcTypes, true},
			{"function", bcFunction(), bcNames, bcTypes, false},
			{"renamed", bcFrom("bc_part.pg_buffercache_pages", renamed, bcSQLTypes), renamed, bcTypes, false},
			{"eight_prefix", bcFrom("bc_part.pg_buffercache_pages", bcNames[:8], bcSQLTypes[:8]), bcNames[:8], bcTypes[:8], false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, empty := range []bool{false, true} {
					q := "SELECT * FROM " + tc.from
					if empty {
						q += " WHERE false"
					}
					st, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("prepare", err)
					}
					defer st.Close()
					held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
					for i, source := range []fixture.Executor{c, held, held} {
						t.Run(fmt.Sprintf("empty_%v/route_%d", empty, i), func(t *testing.T) {
							rows, err := source.QueryContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							got := bcRows(t, rows, b.PG, tc.view, tc.names, tc.types)
							if empty {
								if len(got) != 0 {
									t.Fatal("WHERE false emitted rows", got)
								}
								return
							}
							if int64(len(got)) != capacity {
								t.Fatal("one row per shared buffer", len(got), capacity)
							}
							seen := make(map[int64]bool, len(got))
							for _, row := range got {
								id, ok := row[0].(int64)
								if !ok || id < 1 || id > capacity || seen[id] {
									t.Fatal("bufferid uniqueness/domain", row)
								}
								seen[id] = true
							}
						})
					}
				}
			})
		}
		// Ordinary view declarations are nullable, regardless of current values.
		want := make([][]any, len(bcNames))
		for i := range bcNames {
			oids := []int64{23, 26, 26, 26, 21, 20, 16, 21, 23}
			want[i] = []any{int64(i + 1), bcNames[i], oids[i], false}
		}
		bcBoth(t, p, b, c, fixture.Select("SELECT attnum::integer AS n,attname::text AS name,atttypid::bigint AS type,attnotnull FROM pg_attribute WHERE attrelid='bc_part.pg_buffercache'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"n", "name", "type", "attnotnull"}, []string{"INT4", "TEXT", "INT8", "BOOL"}, want))
	})
}
