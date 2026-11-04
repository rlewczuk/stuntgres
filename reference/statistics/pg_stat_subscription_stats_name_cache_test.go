package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsNameFilteredCache(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, route := range []string{"direct", "held"} {
			t.Run(mode+"/"+route, func(t *testing.T) {
				const localName = "stat_stats_name_a"
				const peerName = "stat_stats_name_b"
				const peerDatabase = "stat_stats_name_peer"
				databases := []string{peerDatabase}
				fixture.DatabaseReferenceCleanup(t, &databases)
				statSubscriptionScenario(t, []string{localName}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					statSubscriptionCreate(t, p, b, c, localName)
					localID := statSubscriptionID(t, p, c, localName)
					statSubscriptionExec(t, p, b, c, "CREATE DATABASE "+peerDatabase)
					pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, peerDatabase, "")
					defer pool.Close()
					peer, err := pool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					// A database containing a subscription cannot be dropped. Retire
					// the peer declaration before returning its connection and pool.
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						if _, err := peer.ExecContext(ctx, "DROP SUBSCRIPTION IF EXISTS "+peerName); err != nil {
							t.Error(err)
						}
					}()
					pb := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder, PG: b.PG}
					statSubscriptionCreate(t, p, pb, peer, peerName)
					peerID := statSubscriptionID(t, p, peer, peerName)
					if localID == peerID {
						t.Fatal("distinct actual database-qualified subscription identities", localID, peerID)
					}
					initial := [][]any{statSubscriptionStatsRow(localID, localName), statSubscriptionStatsRow(peerID, peerName)}
					statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, initial)
					statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, initial)

					// Start with positive A history, not a missing fact that could be
					// reconstructed as zero/NULL. Each reset helper observes independent
					// lower/upper clock bounds on the resetting peer connection.
					resetA0 := statSubscriptionStatsReset(t, p, peer, localID)
					if resetA0.IsZero() {
						t.Fatal("initial A reset must be a real nonzero instant")
					}
					statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, [][]any{
						statSubscriptionStatsRow(localID, localName, resetA0),
						statSubscriptionStatsRow(peerID, peerName),
					})
					statSubscriptionExec(t, p, b, c, "SET stats_fetch_consistency="+mode)
					// subname is catalog-owned, unlike getter-produced subid. Leave
					// $1 uncast so the existing binder infers the NAME parameter.
					query := statSubscriptionStatsProjection() + " WHERE subname=$1"
					var resetA1, resetB1 time.Time
					for epoch := range 2 {
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						statSubscriptionStatsTimestamp(t, p, tx, time.Time{})
						var executor fixture.Executor = tx
						if route == "held" {
							stmt, err := tx.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: query}
						}
						// Preparing/describing the transaction-owned statement alone
						// must not establish a cumulative cut.
						statSubscriptionStatsTimestamp(t, p, tx, time.Time{})
						read := func(name string, want []any) {
							t.Helper()
							for range 2 {
								statSubscriptionStatsCheck(t, p, b, executor, query, [][]any{want}, name)
							}
						}
						firstA := statSubscriptionStatsRow(localID, localName, resetA0)
						if epoch != 0 {
							firstA = statSubscriptionStatsRow(localID, localName, resetA1)
						}
						read(localName, firstA)
						stamp := statSubscriptionStatsTimestampRead(t, p, tx)
						if (mode == "snapshot") == stamp.IsZero() {
							t.Fatal("first name-filtered cumulative timestamp", mode, stamp)
						}
						if epoch == 0 {
							resetB1 = statSubscriptionStatsReset(t, p, peer, peerID)
							if resetB1.IsZero() {
								t.Fatal("B reset must be a real nonzero instant")
							}
						}
						wantB := statSubscriptionStatsRow(peerID, peerName, resetB1)
						if epoch == 0 && mode == "snapshot" {
							// A's first cumulative access captures the complete shared
							// snapshot, even though only A was returned by its name.
							wantB = statSubscriptionStatsRow(peerID, peerName)
						}
						// Cache must see B's new reset on its first requested access:
						// the earlier catalog-side name filter did not demand B.
						read(peerName, wantB)
						statSubscriptionStatsTimestamp(t, p, tx, stamp)
						if epoch == 0 {
							resetA1 = statSubscriptionStatsReset(t, p, peer, localID)
							if resetA1.IsZero() {
								t.Fatal("later A reset must be a real nonzero instant")
							}
						}
						wantA := statSubscriptionStatsRow(localID, localName, resetA1)
						if epoch == 0 && mode != "none" {
							wantA = firstA
						}
						// The same held statement changes A -> B -> A -> B; direct
						// calls repeat the same arguments too. Retaining positive A0
						// rules out clearing the cache on every name-filtered query.
						read(localName, wantA)
						read(peerName, wantB)
						statSubscriptionStatsTimestamp(t, p, tx, stamp)
						if epoch == 0 {
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SELECT pg_stat_clear_snapshot()", 1))
							statSubscriptionStatsTimestamp(t, p, tx, time.Time{})
							read(localName, statSubscriptionStatsRow(localID, localName, resetA1))
							clearedStamp := statSubscriptionStatsTimestampRead(t, p, tx)
							if (mode == "snapshot") == clearedStamp.IsZero() {
								t.Fatal("first cumulative timestamp after clear", mode, clearedStamp)
							}
							read(peerName, statSubscriptionStatsRow(peerID, peerName, resetB1))
							statSubscriptionStatsTimestamp(t, p, tx, clearedStamp)
						}
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						statSubscriptionStatsTimestamp(t, p, c, time.Time{})
					}
				})
			})
		}
	}
}
