package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsSnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"subscription", "table", "table-held", "bgwriter", "bgwriter-held"} {
			t.Run(mode+"/"+first, func(t *testing.T) {
				statSubscriptionScenario(t, []string{"stat_stats_snapshot"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					statSubscriptionCreate(t, p, b, c, "stat_stats_snapshot")
					localID := statSubscriptionID(t, p, c, "stat_stats_snapshot")
					statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, [][]any{statSubscriptionStatsRow(localID, "stat_stats_snapshot")})
					statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_stats_snapshot_table(v integer)")
					defer func() {
						if !b.PG {
							return
						}
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						if _, err := c.ExecContext(ctx, "DROP DATABASE IF EXISTS stat_stats_snapshot_peer"); err != nil {
							t.Error(err)
						}
					}()
					statSubscriptionExec(t, p, b, c, "CREATE DATABASE stat_stats_snapshot_peer")
					pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "stat_stats_snapshot_peer", "")
					defer pool.Close()
					peer, err := pool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					pb := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder, PG: b.PG}
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						if _, err := peer.ExecContext(ctx, "DROP SUBSCRIPTION IF EXISTS stat_stats_snapshot"); err != nil {
							t.Error(err)
						}
					}()
					statSubscriptionCreate(t, p, pb, peer, "stat_stats_snapshot")
					peerID := statSubscriptionID(t, p, peer, "stat_stats_snapshot")
					if peerID == localID {
						t.Fatal("database-qualified distinct identities")
					}
					want := [][]any{statSubscriptionStatsRow(localID, "stat_stats_snapshot"), statSubscriptionStatsRow(peerID, "stat_stats_snapshot")}
					statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, want)
					statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, want)
					query := statSubscriptionStatsSQL + " WHERE subid=$1::oid"
					held := statSubscriptionHeld(t, p, c, query)
					defer held.Statement.Close()
					statSubscriptionExec(t, p, b, c, "SET stats_fetch_consistency="+mode)
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					txStmt, err := tx.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer txStmt.Close()
					txHeld := fixture.HeldStatement{Executor: tx, Statement: txStmt, SQL: query}
					statSubscriptionStatsTimestamp(t, p, tx, time.Time{})
					switch first {
					case "subscription":
						statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL, want)
					case "table", "table-held", "bgwriter", "bgwriter-held":
						firstSQL := "SELECT seq_scan FROM pg_stat_all_tables WHERE relid='stat_stats_snapshot_table'::regclass"
						if first == "bgwriter" || first == "bgwriter-held" {
							firstSQL = "SELECT buffers_clean FROM pg_stat_bgwriter"
						}
						var firstExecutor fixture.Executor = tx
						if first == "table-held" || first == "bgwriter-held" {
							stmt, err := tx.PrepareContext(p.Ctx, firstSQL)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							firstExecutor = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: firstSQL}
						}
						rows, err := firstExecutor.QueryContext(p.Ctx, firstSQL)
						if err != nil {
							t.Fatal(err)
						}
						if !rows.Next() {
							t.Fatal("missing unrelated cumulative row", rows.Err())
						}
						var value int64
						if err := rows.Scan(&value); err != nil || value < 0 {
							t.Fatal("unrelated cumulative counter", value, err)
						}
						if first == "table" || first == "table-held" {
							if value != 0 {
								t.Fatal("new table has no scans", value)
							}
						}
						for rows.Next() {
						}
						if rows.Err() != nil {
							t.Fatal(rows.Err())
						}
						rows.Close()
					}
					stamp := statSubscriptionStatsTimestampRead(t, p, tx)
					if (mode == "snapshot") == stamp.IsZero() {
						t.Fatal("first cumulative timestamp", mode, stamp)
					}
					reset := statSubscriptionStatsReset(t, p, peer, peerID)
					statSubscriptionExec(t, p, b, tx, "SAVEPOINT stats_cut")
					statSubscriptionExec(t, p, b, tx, "ROLLBACK TO stats_cut")
					statSubscriptionStatsTimestamp(t, p, tx, stamp)
					peerWant := statSubscriptionStatsRow(peerID, "stat_stats_snapshot", reset)
					if mode == "snapshot" || mode == "cache" && first == "subscription" {
						peerWant = want[1]
					}
					executors := []fixture.Executor{tx, txHeld, txHeld}
					if first == "table-held" || first == "bgwriter-held" {
						executors = []fixture.Executor{txHeld, tx, txHeld}
					}
					for _, e := range executors {
						statSubscriptionStatsCheck(t, p, b, e, query, [][]any{peerWant}, peerID)
					}
					statSubscriptionStatsTimestamp(t, p, tx, stamp)
					// A previously accessed key is retained only in cache/snapshot.
					localReset := statSubscriptionStatsReset(t, p, peer, localID)
					localWant := statSubscriptionStatsRow(localID, "stat_stats_snapshot", localReset)
					if mode != "none" {
						localWant = want[0]
					}
					statSubscriptionStatsCheck(t, p, b, tx, query, [][]any{localWant}, localID)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SELECT pg_stat_clear_snapshot()", 1))
					statSubscriptionStatsTimestamp(t, p, tx, time.Time{})
					for _, e := range []fixture.Executor{tx, txHeld} {
						statSubscriptionStatsCheck(t, p, b, e, query, [][]any{statSubscriptionStatsRow(peerID, "stat_stats_snapshot", reset)}, peerID)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					statSubscriptionStatsTimestamp(t, p, c, time.Time{})
					statSubscriptionStatsCheck(t, p, b, held, query, [][]any{statSubscriptionStatsRow(localID, "stat_stats_snapshot", localReset)}, localID)
					// A rolled-back reset is still an immediate shared event.
					resetTx, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					reset = statSubscriptionStatsReset(t, p, resetTx, peerID)
					if err := resetTx.Rollback(); err != nil {
						t.Fatal(err)
					}
					statSubscriptionStatsCheck(t, p, b, held, query, [][]any{statSubscriptionStatsRow(peerID, "stat_stats_snapshot", reset)}, peerID)
					// Effective setting changes invalidate a retained cut, including a round trip.
					tx, err = c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					statSubscriptionStatsCheck(t, p, b, tx, query, [][]any{statSubscriptionStatsRow(peerID, "stat_stats_snapshot", reset)}, peerID)
					reset = statSubscriptionStatsReset(t, p, peer, peerID)
					statSubscriptionExec(t, p, b, tx, "SET LOCAL stats_fetch_consistency=none")
					statSubscriptionExec(t, p, b, tx, "SET LOCAL stats_fetch_consistency="+mode)
					statSubscriptionStatsTimestamp(t, p, tx, time.Time{})
					statSubscriptionStatsCheck(t, p, b, tx, query, [][]any{statSubscriptionStatsRow(peerID, "stat_stats_snapshot", reset)}, peerID)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT pg_stat_reset_subscription_stats(0::oid)", fixture.ControlsReferenceDiagnostic("22023", "invalid subscription OID 0")))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT pg_stat_reset_subscription_stats(4294967295::oid)", 1))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT pg_stat_reset_subscription_stats(NULL::oid)", 1))
					var all []time.Time
					rows, err := c.QueryContext(p.Ctx, "SELECT stats_reset FROM pg_stat_subscription_stats")
					if err != nil {
						t.Fatal(err)
					}
					for rows.Next() {
						var at time.Time
						if err := rows.Scan(&at); err != nil {
							t.Fatal(err)
						}
						all = append(all, at)
					}
					if rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					rows.Close()
					if len(all) != 2 || all[0].IsZero() || !all[0].Equal(all[1]) {
						t.Fatal("NULL resets actual cross-database records at one instant", all)
					}
				})
			})
		}
	}
}

func statSubscriptionStatsTimestampRead(t *testing.T, p *fixture.Pair, e fixture.Executor) time.Time {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var at sql.NullTime
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Scan(&at); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return at.Time.UTC()
}
func statSubscriptionStatsTimestamp(t *testing.T, p *fixture.Pair, e fixture.Executor, want time.Time) {
	t.Helper()
	if got := statSubscriptionStatsTimestampRead(t, p, e); !got.Equal(want) {
		t.Fatal("snapshot timestamp", got, want)
	}
}
