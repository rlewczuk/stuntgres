package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceStatDatabaseForeignWorkers(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		run(fixture.Exec("SET stats_fetch_consistency=none", 0))
		read := func(ctx context.Context) (int64, int64) {
			var planned, launched int64
			if err := observer.QueryRowContext(ctx, "SELECT parallel_workers_to_launch,parallel_workers_launched FROM pg_stat_database WHERE datname=current_database()").Scan(&planned, &launched); err != nil {
				t.Fatal(err)
			}
			return planned, launched
		}
		await := func(beforeP, beforeL, wantP, wantL int64) {
			ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
			defer cancel()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				planned, launched := read(ctx)
				if planned-beforeP == wantP && launched-beforeL == wantL {
					return
				}
				select {
				case <-ctx.Done():
					t.Fatal("foreign actual workers", planned-beforeP, launched-beforeL, wantP, wantL, ctx.Err())
				case <-tick.C:
				}
			}
		}
		execute := func(phase string) {
			beforeP, beforeL := read(p.Ctx)
			writer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			for _, q := range []string{"SET max_parallel_workers_per_gather=4", "SET parallel_setup_cost=0", "SET parallel_tuple_cost=0", "SET min_parallel_table_scan_size=0"} {
				if _, err := writer.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			planned, launched := int64(0), int64(0)
			if b.PG {
				rows, err := writer.QueryContext(p.Ctx, "EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) SELECT id FROM fdw_data ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var line string
					if err := rows.Scan(&line); err != nil {
						t.Fatal(err)
					}
					line = strings.TrimSpace(line)
					for _, v := range []struct {
						prefix string
						to     *int64
					}{{"Workers Planned:", &planned}, {"Workers Launched:", &launched}} {
						if strings.HasPrefix(line, v.prefix) {
							n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, v.prefix)), 10, 64)
							if err != nil {
								t.Fatal(err)
							}
							*v.to += n
						}
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			} else {
				r.partitions = 2
				planned = int64(min(4, max(2, runtime.GOMAXPROCS(0))))
				launched = 2
				if phase != "success" {
					failure := errors.New("real foreign scan failed at " + phase)
					r.hook = func(ctx context.Context, at string, index int) error {
						if at == phase && (index <= 0 || phase == "Partition") {
							return failure
						}
						return ctx.Err()
					}
					fdwHostQueryError(t, p.Ctx, writer, "SELECT id FROM fdw_data ORDER BY id", failure)
					r.hook = nil
					if phase == "Begin" || phase == "Partition" {
						launched = 0
					}
				} else {
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
				}
				fdwParallelBalanced(t, r)
			}
			if err := writer.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			await(beforeP, beforeL, planned, launched)
		}
		execute("success")
		if !b.PG {
			for _, phase := range []string{"Begin", "Partition", "Iterate"} {
				execute(phase)
			}
		}
	})
}
