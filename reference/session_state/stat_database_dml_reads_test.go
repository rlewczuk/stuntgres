package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceStatDatabaseDMLReads(t *testing.T) {
	statDatabaseScenario(t, "dml_reads", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		for _, tc := range []struct {
			name, query string
			nativeReads int64
		}{
			{"update", "UPDATE stat_db_rows SET id=id WHERE id<0", 6},
			{"delete", "DELETE FROM stat_db_rows WHERE id<0", 6},
			{"update_from", "UPDATE stat_db_rows SET id=stat_db_rows.id FROM (VALUES(100)) AS s(id) WHERE stat_db_rows.id=s.id", 6},
			{"delete_using", "DELETE FROM stat_db_rows USING (VALUES(100)) AS s(id) WHERE stat_db_rows.id=s.id", 6},
			{"merge", "MERGE INTO stat_db_rows AS t USING (VALUES(100),(200)) AS s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET id=s.id", 12},
		} {
			target := create(tc.name)
			statDatabaseSetup(t, p, target, observer)
			setup, endSetup := statDatabaseConn(t, p, target)
			statsExec(t, p, target, setup, "INSERT INTO stat_db_rows VALUES(1),(2),(3),(4),(5),(6)", 6)
			endSetup()
			base := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == 2 })
			read := func(ctx context.Context) (int64, int64) {
				var returned, fetched int64
				if err := observer.QueryRowContext(ctx, "SELECT tup_returned,tup_fetched FROM pg_stat_database WHERE datname=$1", target.Name).Scan(&returned, &fetched); err != nil {
					t.Fatal(err)
				}
				return returned, fetched
			}
			beforeReturned, beforeFetched := read(p.Ctx)
			writer, retire := statDatabaseConn(t, p, target)
			statsExec(t, p, target, writer, "SET max_parallel_workers_per_gather=0", 0)
			statsExec(t, p, target, writer, tc.query, 0)
			retire()
			after := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == base.Sessions+1 })
			// Zero affected rows must not suppress genuine target input. Native reads
			// follow its actual direct target loops (MERGE physically rescans twice).
			// PostgreSQL can choose another join strategy and can also read catalogs;
			// each real plan must still read the six live target rows independently.
			ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
			defer cancel()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				returned, fetched := read(ctx)
				enough := returned-beforeReturned >= 6
				if !b.PG {
					enough = returned-beforeReturned == tc.nativeReads && fetched == beforeFetched
				}
				if enough {
					break
				}
				select {
				case <-ctx.Done():
					tick.Stop()
					cancel()
					t.Fatal("actual target reads", tc.name, returned-beforeReturned, fetched-beforeFetched, ctx.Err())
				case <-tick.C:
				}
			}
			tick.Stop()
			cancel()
			if after.Insert != base.Insert || after.Update != base.Update || after.Delete != base.Delete {
				t.Fatal("predicate-rejected target mutated", base, after)
			}
			verify, endVerify := statDatabaseConn(t, p, target)
			fixture.RunStep(t, p.Ctx, verify, target.Recorder, fixture.Select("SELECT id FROM stat_db_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}}))
			endVerify()
		}
	})
}
