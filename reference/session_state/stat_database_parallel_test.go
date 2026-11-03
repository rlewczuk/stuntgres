package reference_test

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatDatabaseParallel(t *testing.T) {
	statDatabaseScenario(t, "parallel", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		target := create("target")
		statDatabaseSetup(t, p, target, observer)
		setup, endSetup := statDatabaseConn(t, p, target)
		statsExec(t, p, target, setup, "INSERT INTO stat_db_rows VALUES(1),(2),(3),(4),(5),(6)", 6)
		endSetup()
		base := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == 2 })
		writer, end := statDatabaseConn(t, p, target)
		for _, q := range []string{"SET max_parallel_workers_per_gather=2", "SET min_parallel_table_scan_size=0", "SET parallel_setup_cost=0", "SET parallel_tuple_cost=0"} {
			statsExec(t, p, target, writer, q, 0)
		}
		// Inspection is not execution, even when the planner proposes workers.
		rows, err := writer.QueryContext(p.Ctx, "EXPLAIN SELECT id FROM stat_db_rows")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		planned, launched := int64(0), int64(0)
		// Native count(*) is serial-only; string_agg has genuine partial-state
		// combine/transport support, independently of this statistics feature.
		for _, q := range []string{"SELECT id FROM stat_db_rows", "SELECT string_agg(id::text,','::text) FROM stat_db_rows"} {
			if b.PG {
				rows, err := writer.QueryContext(p.Ctx, "EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) "+q)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var line string
					if err := rows.Scan(&line); err != nil {
						t.Fatal(err)
					}
					line = strings.TrimSpace(line)
					for _, field := range []struct {
						prefix      string
						destination *int64
					}{{"Workers Planned:", &planned}, {"Workers Launched:", &launched}} {
						if strings.HasPrefix(line, field.prefix) {
							n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, field.prefix)), 10, 64)
							if err != nil {
								t.Fatal(err)
							}
							*field.destination += n
						}
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			} else {
				rows, err := writer.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					if strings.Contains(q, "string_agg") {
						var value string
						if err := rows.Scan(&value); err != nil {
							t.Fatal(err)
						}
						items := strings.Split(value, ",")
						sort.Strings(items)
						if strings.Join(items, ",") != "1,2,3,4,5,6" {
							t.Fatal("actual partial aggregate result", value)
						}
					} else {
						var n int64
						if err := rows.Scan(&n); err != nil {
							t.Fatal(err)
						}
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				want := 6
				if strings.Contains(q, "string_agg") {
					want = 1
				}
				if count != want {
					t.Fatal(q, count, want)
				}
				planned += 2
				launched += 2
			}
		}
		// A real serial execution is an independent zero-worker control.
		statsExec(t, p, target, writer, "SET max_parallel_workers_per_gather=0", 0)
		fixture.RunStep(t, p.Ctx, writer, target.Recorder, fixture.Select("SELECT count(*) AS n FROM stat_db_rows", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		end()
		got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == base.Sessions+1 })
		if got.Planned-base.Planned != planned || got.Launched-base.Launched != launched {
			t.Fatal("actual admitted executor workers", base, got, planned, launched)
		}
	})
}
