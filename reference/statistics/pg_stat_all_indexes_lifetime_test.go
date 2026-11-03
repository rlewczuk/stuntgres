package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGStatAllIndexesSnapshotsAndLifetime(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range []string{"none", "cache", "snapshot"} {
			t.Run(mode, func(t *testing.T) {
				tableA, indexA := "stat_snap_"+mode+"_a", "stat_snap_"+mode+"_ai"
				tableB, indexB := "stat_snap_"+mode+"_b", "stat_snap_"+mode+"_bi"
				indexStatCreate(t, p, b, c, tableA, indexA)
				indexStatCreate(t, p, b, c, tableB, indexB)
				reader := fixture.IdentityConnection(t, p.Ctx, b.DB)
				fixture.StatisticsExec(t, p, b, reader, "SET stats_fetch_consistency="+mode, 0)
				fixture.StatisticsExec(t, p, b, reader, "BEGIN", 0)
				defer reader.ExecContext(context.Background(), "ROLLBACK")
				a := indexStatGet(t, p, reader, indexA)
				indexStatWant(t, a, 0, 0, 0)
				writer, retire := fixture.StatisticsReportingConn(t, p, b)
				indexStatPrefer(t, p, b, writer)
				for _, table := range []string{tableA, tableB} {
					q := "SELECT payload FROM " + table + " WHERE n=2"
					if !indexStatSimple(t, b, indexStatPlan(t, p, writer, q)) {
						t.Fatal("snapshot writer requires observed simple search")
					}
					indexStatPayloads(t, p, b, writer, q, []string{"a", "b"})
				}
				retire()
				indexStatAwait(t, p, b, indexA, 1, 2, 2)
				indexStatAwait(t, p, b, indexB, 1, 2, 2)
				wantA, wantB := int64(0), int64(1)
				if mode == "none" {
					wantA = 1
				}
				if mode == "snapshot" {
					wantB = 0
				}
				gotA, gotB := indexStatGet(t, p, reader, indexA), indexStatGet(t, p, reader, indexB)
				if gotA.Scans != wantA || gotB.Scans != wantB {
					t.Fatal("shared Reader source selection", mode, gotA, gotB)
				}
				fixture.StatisticsClearSnapshot(t, p, b, reader)
				indexStatWant(t, indexStatGet(t, p, reader, indexA), 1, 2, 2)
				indexStatWant(t, indexStatGet(t, p, reader, indexB), 1, 2, 2)
				// A clear is neither a publication request nor a reset. Native's explicit
				// query is an authenticated fallback; PG's actual simple read is pending.
				pending, retirePending := fixture.StatisticsReportingConn(t, p, b)
				indexStatPrefer(t, p, b, pending)
				fixture.StatisticsExec(t, p, b, pending, "BEGIN", 0)
				indexStatPayloads(t, p, b, pending, "SELECT payload FROM "+tableA+" WHERE n=2", []string{"a", "b"})
				fixture.StatisticsClearSnapshot(t, p, b, reader)
				indexStatWant(t, indexStatGet(t, p, reader, indexA), 1, 2, 2)
				fixture.StatisticsExec(t, p, b, pending, "ROLLBACK", 0)
				retirePending()
				final := int64(1)
				if b.PG {
					final = 2
				}
				indexStatAwait(t, p, b, indexA, final, 2*final, 2*final)
				fixture.StatisticsExec(t, p, b, reader, "COMMIT", 0)
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				fixture.StatisticsExec(t, p, b, c, "DROP INDEX "+indexA, 0)
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
				existing := indexStatGet(t, p, c, indexA)
				if existing.Index != a.Index || existing.Scans != final {
					t.Fatal("drop rollback changed identity/history", existing)
				}
				fixture.StatisticsExec(t, p, b, c, "ALTER INDEX "+indexA+" RENAME TO "+indexA+"_renamed", 0)
				moved := indexStatGet(t, p, c, indexA+"_renamed")
				if moved.Index != a.Index || moved.Scans != final {
					t.Fatal("rename history", moved)
				}
				fixture.StatisticsExec(t, p, b, c, "DROP INDEX "+indexA+"_renamed", 0)
				if _, err := indexStatRead(p.Ctx, c, indexA+"_renamed"); err != sql.ErrNoRows {
					t.Fatal("drop row remains", err)
				}
				fixture.StatisticsExec(t, p, b, c, "CREATE INDEX "+indexA+" ON "+tableA+" USING hash(n)", 0)
				fresh := indexStatGet(t, p, c, indexA)
				if fresh.Index == a.Index || fresh.Parent != a.Parent {
					t.Fatal("new identity after recreate", fresh, a)
				}
				indexStatWant(t, fresh, 0, 0, 0)
				if a.Scans != 0 || a.Last.Valid {
					t.Fatal("retained row mutated", a)
				}
			})
		}
	})
}

func indexStatClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	var at time.Time
	if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}
func TestReferencePGStatAllIndexesTransactionStop(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexStatCreate(t, p, b, c, "stat_stop", "stat_stop_hash")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		var scans int64
		for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
			writer, retire := fixture.StatisticsReportingConn(t, p, b)
			indexStatPrefer(t, p, b, writer)
			fixture.StatisticsExec(t, p, b, writer, "BEGIN", 0)
			q := "SELECT payload FROM stat_stop WHERE n=2"
			method := indexStatPlan(t, p, writer, q)
			if b.PG {
				if !indexStatSimple(t, b, method) {
					t.Fatal("explicit PG stop witness not simple", method)
				}
			}
			start := indexStatClock(t, p, writer)
			indexStatPayloads(t, p, b, writer, q, []string{"a", "b"})
			// Server clock barriers are independent of the client/remote clock offset.
			// Exact equality and delayed publication use virtual-time composed tests.
			lower := indexStatClock(t, p, writer)
			if !lower.After(start) {
				t.Fatal("terminal clock did not advance", start, lower)
			}
			current := indexStatGet(t, p, c, "stat_stop_hash")
			if current.Scans != scans {
				t.Fatal("pending work published inside transaction", current, scans)
			}
			fixture.StatisticsExec(t, p, b, writer, terminal, 0)
			retire()
			upper := indexStatClock(t, p, c)
			if b.PG {
				scans++
			}
			r := indexStatAwait(t, p, b, "stat_stop_hash", scans, 2*scans, 2*scans)
			if b.PG && (!r.Last.Valid || r.Last.Time.Before(lower) || r.Last.Time.After(upper)) {
				t.Fatal("last scan not transaction stop bracket", start, lower, upper, r)
			}
		}
		if !b.PG {
			// Positive native implicit work has a public terminal publication witness;
			// exact scan-start/Finish separation is exercised by the held-stage unit.
			writer, retire := fixture.StatisticsReportingConn(t, p, b)
			indexStatPrefer(t, p, b, writer)
			indexStatPayloads(t, p, b, writer, "SELECT payload FROM stat_stop WHERE n=2", []string{"a", "b"})
			retire()
			indexStatAwait(t, p, b, "stat_stop_hash", 1, 2, 2)
		} else {
			var limit int
			if err := c.QueryRowContext(p.Ctx, "SHOW max_prepared_transactions").Scan(&limit); err != nil {
				t.Fatal(err)
			}
			if limit > 0 {
				writer, retire := fixture.StatisticsReportingConn(t, p, b)
				indexStatPrefer(t, p, b, writer)
				fixture.StatisticsExec(t, p, b, writer, "BEGIN", 0)
				indexStatPayloads(t, p, b, writer, "SELECT payload FROM stat_stop WHERE n=2", []string{"a", "b"})
				lower := indexStatClock(t, p, writer)
				gid := fmt.Sprintf("index_stats_%d", fixture.LockPID(t, p.Ctx, writer))
				fixture.StatisticsExec(t, p, b, writer, "PREPARE TRANSACTION '"+gid+"'", 0)
				retire()
				upper := indexStatClock(t, p, c)
				r := indexStatAwait(t, p, b, "stat_stop_hash", scans+1, 2*(scans+1), 2*(scans+1))
				if r.Last.Time.Before(lower) || r.Last.Time.After(upper) {
					t.Fatal("preparer stop ownership", r)
				}
				fixture.StatisticsExec(t, p, b, c, "COMMIT PREPARED '"+gid+"'", 0)
			}
		}
	})
}
