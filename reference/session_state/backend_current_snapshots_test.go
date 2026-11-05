package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendCurrentSnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, ioFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ioFirst=%t", mode, ioFirst), func(t *testing.T) {
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
					admin := statActivityConn(t, p, b)
					peer := statActivityConn(t, p, b)
					pid := statActivityPID(t, p, peer)
					activityExec(t, p, o, "SET stats_fetch_consistency="+mode)
					activityExec(t, p, o, "BEGIN")
					defer o.ExecContext(context.Background(), "ROLLBACK")
					before := backendStamp(t, p, b.PG, o, pid, ioFirst)
					if before != nil {
						t.Fatal("fresh reset", before)
					}
					lo, hi := backendReset(t, p, admin, pid)
					current := backendBound(t, backendStamp(t, p, b.PG, admin, pid, true), lo, hi)
					got := backendStamp(t, p, b.PG, o, pid, !ioFirst)
					if mode == "none" {
						if !reflect.DeepEqual(got, current) {
							t.Fatal("none stale", got, current)
						}
					} else if got != nil {
						t.Fatal("IO/WAL must share whole cached entry", got)
					}
					newcomer := statActivityConn(t, p, b)
					newPID := statActivityPID(t, p, newcomer)
					if len(backendQuery(t, p, b.PG, o, 1, newPID)) != 0 {
						t.Fatal("new backend entered retained activity cut")
					}
					backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, newPID), b.PG, false)
					statActivityClear(t, p, o)
					if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, true), current) || !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), current) {
						t.Fatal("clear didn't refresh both families")
					}
					backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, newPID), b.PG)
					// Real physical retirement, not returning a database/sql borrower.
					statActivitySignal(t, p, admin, pid, true)
					statActivityRetired(t, p, admin, pid)
					if len(backendQuery(t, p, b.PG, o, 0, pid)) != 1 {
						t.Fatal("ended row should remain in retained activity")
					}
					if len(backendQuery(t, p, b.PG, o, 1, pid)) != 0 {
						t.Fatal("cached positive defeated current liveness")
					}
					backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, pid), b.PG, false)
					for _, end := range []string{"COMMIT", "ROLLBACK"} {
						activityExec(t, p, o, end)
						if len(backendQuery(t, p, b.PG, o, 0, pid)) != 0 {
							t.Fatal("transaction end retained retired status")
						}
						activityExec(t, p, o, "BEGIN")
						backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, newPID), b.PG)
					}
				})
			})
		}
	}
	t.Run("PeerCumulativeFirst", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
			defer o.ExecContext(context.Background(), "DROP DATABASE IF EXISTS bc_snapshot_peer")
			activityExec(t, p, o, "CREATE DATABASE bc_snapshot_peer")
			pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, "bc_snapshot_peer", "")
			defer pool.Close()
			peer, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			pid := statActivityPID(t, p, peer)
			backendReset(t, p, peer, pid)
			old := backendStamp(t, p, b.PG, peer, pid, false)
			activityExec(t, p, o, "SET stats_fetch_consistency=snapshot")
			activityExec(t, p, o, "BEGIN")
			defer o.ExecContext(context.Background(), "ROLLBACK")
			// COUNT(*) can prune every cumulative view expression in PostgreSQL.
			// Demand a real cumulative field, then prove the snapshot exists
			// before changing the peer's backend entry.
			if n := backendCount(t, p, o, "SELECT tup_inserted FROM pg_stat_database WHERE datname=current_database()"); n < 0 {
				t.Fatal("nonnegative cumulative database field", n)
			}
			var captured sql.NullTime
			if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&captured); err != nil || !captured.Valid {
				t.Fatal("demanded cumulative field did not establish snapshot", captured, err)
			}
			lo, hi := backendReset(t, p, peer, pid)
			current := backendBound(t, backendStamp(t, p, b.PG, peer, pid, false), lo, hi)
			if reflect.DeepEqual(current, old) {
				t.Fatal("peer reset did not distinguish the retained cut", old, current)
			}
			for _, io := range []bool{true, false} {
				if got := backendStamp(t, p, b.PG, o, pid, io); !reflect.DeepEqual(got, old) {
					t.Fatal("cumulative-first cut omitted owning peer backend", io, got, old, current)
				}
				var retained sql.NullTime
				if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&retained); err != nil || !retained.Valid || !retained.Time.Equal(captured.Time) {
					t.Fatal("backend getter replaced the established snapshot", retained, captured, err)
				}
			}
			statActivityClear(t, p, o)
			var cleared sql.NullTime
			if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&cleared); err != nil || cleared.Valid {
				t.Fatal("clear retained the cumulative snapshot", cleared, err)
			}
			if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), current) {
				t.Fatal("clear did not refresh owning peer backend")
			}
			activityExec(t, p, o, "ROLLBACK")
		})
	})
}

func TestReferenceBackendCurrentReset(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		other := statActivityConn(t, p, b)
		pid, otherPID := statActivityPID(t, p, peer), statActivityPID(t, p, other)
		if backendStamp(t, p, b.PG, o, pid, true) != nil || backendStamp(t, p, b.PG, o, pid, false) != nil {
			t.Fatal("birth minted reset timestamp")
		}
		shared := func() [2]any {
			var out [2]any
			if err := o.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_stat_io LIMIT 1").Scan(&out[0]); err != nil {
				t.Fatal(err)
			}
			if err := o.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_stat_wal").Scan(&out[1]); err != nil {
				t.Fatal(err)
			}
			return out
		}
		original := shared()
		otherStamp := backendStamp(t, p, b.PG, o, otherPID, false)
		lo, hi := backendReset(t, p, o, pid)
		stamp := backendBound(t, backendStamp(t, p, b.PG, o, pid, true), lo, hi)
		if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) || !reflect.DeepEqual(backendStamp(t, p, b.PG, o, otherPID, false), otherStamp) || !reflect.DeepEqual(shared(), original) {
			t.Fatal("backend reset crossed authority")
		}
		for _, family := range []string{"io", "wal"} {
			activityExec(t, p, o, "SELECT pg_stat_reset_shared('"+family+"')")
		}
		if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, true), stamp) || !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) {
			t.Fatal("shared reset replaced backend reset")
		}
		backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, pid), b.PG)
		backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, pid), b.PG, true)
		activityExec(t, p, o, "BEGIN")
		lo, hi = backendReset(t, p, o, pid)
		activityExec(t, p, o, "ROLLBACK")
		backendBound(t, backendStamp(t, p, b.PG, o, pid, false), lo, hi)
		activityExec(t, p, peer, "CREATE TEMP TABLE bc_physical(v integer)")
		activityExec(t, p, peer, "INSERT INTO bc_physical VALUES(1),(2)")
		activityExec(t, p, peer, "SELECT * FROM bc_physical")
		backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, pid), b.PG)
		backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, pid), b.PG, true)
	})
}
