package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsControls(t *testing.T) {
	t.Run("History", func(t *testing.T) {
		statSubscriptionScenario(t, []string{"stat_stats_control"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			statSubscriptionCreate(t, p, b, c, "stat_stats_control")
			id := statSubscriptionID(t, p, c, "stat_stats_control")
			reset := statSubscriptionStatsReset(t, p, c, id)
			statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_sentinel(v integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO stat_subscription_sentinel VALUES(37)", 1))
			defer func() {
				if !b.PG {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				if _, err := c.ExecContext(ctx, "DROP DATABASE IF EXISTS stat_stats_control_peer"); err != nil {
					t.Error(err)
				}
			}()
			statSubscriptionExec(t, p, b, c, "CREATE DATABASE stat_stats_control_peer")
			pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "stat_stats_control_peer", "")
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
				if _, err := peer.ExecContext(ctx, "DROP SUBSCRIPTION IF EXISTS stat_stats_control"); err != nil {
					t.Error(err)
				}
			}()
			statSubscriptionCreate(t, p, pb, peer, "stat_stats_control")
			peerID := statSubscriptionID(t, p, peer, "stat_stats_control")
			peerReset := statSubscriptionStatsReset(t, p, peer, peerID)
			if id == peerID || reset.Equal(peerReset) {
				t.Fatal("independent actual identities/reset events", id, peerID, reset, peerReset)
			}
			localWant := statSubscriptionStatsRow(id, "stat_stats_control", reset)
			peerWant := statSubscriptionStatsRow(peerID, "stat_stats_control", peerReset)
			want := [][]any{localWant, peerWant}
			held := statSubscriptionHeld(t, p, c, statSubscriptionStatsSQL)
			defer held.Statement.Close()
			for _, e := range []fixture.Executor{c, held, held} {
				statSubscriptionStatsCheck(t, p, b, e, statSubscriptionStatsSQL, want)
			}
			statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, want)
			if b.PG {
				return
			} // Native control operations have no PostgreSQL equivalent.
			old, err := c.QueryContext(p.Ctx, statSubscriptionStatsSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			statSubscriptionStatsMetadata(t, old, false)
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "CREATE SUBSCRIPTION") || strings.Contains(dump.String(), "pg_stat_subscription_stats") || strings.Contains(dump.String(), "apply_error_count") || strings.Contains(dump.String(), reset.Format(time.RFC3339Nano)) {
				t.Fatal("definition-only dump", dump.String())
			}
			unchanged := func() {
				t.Helper()
				statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
				statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, want)
				statSubscriptionSentinel(t, p, b, c)
			}
			failure := errors.New("subscription stats control I/O")
			if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			unchanged()
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			unchanged()
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE TABLE broken(")); err == nil {
				t.Fatal("late-statement Restore succeeded")
			}
			unchanged()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer expire()
			for _, tc := range []struct {
				ctx context.Context
				err error
			}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				if err := stuntgres.Restore(tc.ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, tc.err) {
					t.Fatal(err, tc.err)
				}
				unchanged()
				if err := stuntgres.Reset(tc.ctx, p.Native); !errors.Is(err, tc.err) {
					t.Fatal(err, tc.err)
				}
				unchanged()
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fresh := statSubscriptionID(t, p, c, "stat_stats_control")
			if fresh == id || fresh == peerID {
				t.Fatal("Restore reused identity", fresh, id, peerID)
			}
			want = [][]any{statSubscriptionStatsRow(fresh, "stat_stats_control"), peerWant}
			statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
			statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, want)
			statSubscriptionSentinel(t, p, b, c)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, [][]any{peerWant})
			statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, [][]any{peerWant})
			if len(metadata) != 12 || metadata[0].Name() != "subid" || metadata[11].DatabaseTypeName() != "TIMESTAMPTZ" {
				t.Fatal("retained metadata changed", metadata)
			}
			statSubscriptionStatsRead(t, old, false, [][]any{localWant, peerWant})
		})
	})
	t.Run("ACL", statSubscriptionStatsACLControls)
}
