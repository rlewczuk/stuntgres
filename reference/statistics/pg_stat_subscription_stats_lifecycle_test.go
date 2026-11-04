package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsLifecycle(t *testing.T) {
	statSubscriptionScenario(t, []string{"stat_sub_a", "StatSubMixed", "stat_sub_renamed", "stat_sub_private", "stat_sub_savepoint", "stat_sub_enabled", "stat_sub_external"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statSubscriptionCreate(t, p, b, c, "stat_sub_a")
		statSubscriptionCreate(t, p, b, c, `"StatSubMixed"`)
		a, mixed := statSubscriptionID(t, p, c, "stat_sub_a"), statSubscriptionID(t, p, c, "StatSubMixed")
		if a == mixed {
			t.Fatal("distinct subscriptions share identity")
		}
		want := [][]any{statSubscriptionStatsRow(a, "stat_sub_a"), statSubscriptionStatsRow(mixed, "StatSubMixed")}
		held := statSubscriptionHeld(t, p, c, statSubscriptionStatsSQL)
		defer held.Statement.Close()
		for _, e := range []fixture.Executor{c, held, held} {
			statSubscriptionStatsCheck(t, p, b, e, statSubscriptionStatsSQL, want)
		}
		for _, query := range []string{statSubscriptionStatsSQL + " WHERE subname=$1", statSubscriptionStatsSQL + " WHERE subid=$1::oid"} {
			filtered := statSubscriptionHeld(t, p, c, query)
			arg := any("stat_sub_a")
			if query == statSubscriptionStatsSQL+" WHERE subid=$1::oid" {
				arg = a
			}
			for _, e := range []fixture.Executor{c, filtered, filtered} {
				statSubscriptionStatsCheck(t, p, b, e, query, want[:1], arg)
			}
			filtered.Statement.Close()
		}
		projection := fixture.Select("SELECT s.subname AS name,s.subid,s.subid,s.apply_error_count FROM pg_stat_subscription_stats s WHERE s.apply_error_count=0 ORDER BY s.subname", []string{"name", "subid", "subid", "apply_error_count"}, []string{"NAME", "OID", "OID", "INT8"}, [][]any{{"StatSubMixed", mixed, mixed, int64(0)}, {"stat_sub_a", a, a, int64(0)}})
		fixture.PgDescriptionExecutorRows(t, p, b, c, projection)
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(projection))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_subscription_stats", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		statSubscriptionCheck(t, p, b, c, statSubscriptionSQL, [][]any{statSubscriptionRow(a, "stat_sub_a"), statSubscriptionRow(mixed, "StatSubMixed")})
		statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_local(v integer)")
		for _, step := range []fixture.Step{fixture.Exec("INSERT INTO stat_subscription_local VALUES(1)", 1), fixture.Exec("UPDATE stat_subscription_local SET v=2", 1), fixture.Exec("DELETE FROM stat_subscription_local", 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
		}
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_a CONNECTION 'host=publisher.invalid password=changed'")
		reset := statSubscriptionStatsReset(t, p, c, a)
		want[0] = statSubscriptionStatsRow(a, "stat_sub_a", reset)
		old, err := c.QueryContext(p.Ctx, statSubscriptionStatsSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldWant := append([][]any(nil), want...)
		if b.PG {
			statSubscriptionStatsRead(t, old, true, oldWant)
		}
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_a RENAME TO stat_sub_renamed")
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_renamed SET (binary=true,streaming=off)")
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_renamed SET PUBLICATION other,p WITH (refresh=false)")
		want[0] = statSubscriptionStatsRow(a, "stat_sub_renamed", reset)
		if got := statSubscriptionID(t, p, c, "stat_sub_renamed"); got != a {
			t.Fatal("rename reminted identity", got, a)
		}
		statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)

		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		statSubscriptionCreate(t, p, b, tx, "stat_sub_private")
		private := statSubscriptionID(t, p, tx, "stat_sub_private")
		privateWant := append(append([][]any(nil), want...), statSubscriptionStatsRow(private, "stat_sub_private"))
		statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL, privateWant)
		statSubscriptionStatsCheck(t, p, b, peer, statSubscriptionStatsSQL, want)
		statSubscriptionExec(t, p, b, tx, "SAVEPOINT stats_create")
		statSubscriptionCreate(t, p, b, tx, "stat_sub_savepoint")
		aborted := statSubscriptionID(t, p, tx, "stat_sub_savepoint")
		statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL+" WHERE subid=$1::oid", [][]any{statSubscriptionStatsRow(aborted, "stat_sub_savepoint")}, aborted)
		statSubscriptionExec(t, p, b, tx, "ROLLBACK TO stats_create")
		statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL+" WHERE subid=$1::oid", nil, aborted)
		statSubscriptionExec(t, p, b, tx, "SAVEPOINT status_change")
		statSubscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION stat_sub_renamed RENAME TO stat_sub_a")
		statSubscriptionExec(t, p, b, tx, `DROP SUBSCRIPTION "StatSubMixed"`)
		statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL, [][]any{statSubscriptionStatsRow(a, "stat_sub_a", reset), statSubscriptionStatsRow(private, "stat_sub_private")})
		statSubscriptionStatsCheck(t, p, b, peer, statSubscriptionStatsSQL, want)
		statSubscriptionExec(t, p, b, tx, "ROLLBACK TO status_change")
		statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL, privateWant)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
		tx, err = c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		statSubscriptionCreate(t, p, b, tx, "stat_sub_private")
		private = statSubscriptionID(t, p, tx, "stat_sub_private")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		want = append(want, statSubscriptionStatsRow(private, "stat_sub_private"))
		statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
		statSubscriptionStatsCheck(t, p, b, peer, statSubscriptionStatsSQL, want)
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_private")
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_renamed")
		statSubscriptionCreate(t, p, b, c, "stat_sub_renamed")
		fresh := statSubscriptionID(t, p, c, "stat_sub_renamed")
		if fresh == a {
			t.Fatal("drop/recreate reused identity")
		}
		want = [][]any{statSubscriptionStatsRow(fresh, "stat_sub_renamed"), statSubscriptionStatsRow(mixed, "StatSubMixed")}
		statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)

		// An enabled desired state is inspected before commit, so the released
		// server never launches a dummy publisher worker during this witness.
		statSubscriptionExec(t, p, b, c, "CREATE SUBSCRIPTION stat_sub_enabled CONNECTION '' PUBLICATION p WITH (connect=false)")
		enabled := statSubscriptionID(t, p, c, "stat_sub_enabled")
		tx, err = c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		statSubscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION stat_sub_enabled ENABLE")
		statSubscriptionStatsCheck(t, p, b, tx, statSubscriptionStatsSQL+" WHERE subid=$1::oid", [][]any{statSubscriptionStatsRow(enabled, "stat_sub_enabled")}, enabled)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_enabled SET (slot_name=NONE)")
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_enabled")
		if !b.PG {
			statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_sentinel(v integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO stat_subscription_sentinel VALUES(37)", 1))
			step := fixture.ErrorDiagnostic("CREATE SUBSCRIPTION stat_sub_external CONNECTION '' PUBLICATION p", fixture.ControlsReferenceDiagnostic("0A000", "subscription publisher connections are not available"))
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
			statSubscriptionSentinel(t, p, b, c)
		}
		statSubscriptionStatsCrossDatabase(t, p, b, c, want)
		if !b.PG {
			statSubscriptionStatsRead(t, old, false, oldWant)
		}
	})
}

func statSubscriptionStatsCrossDatabase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, local [][]any) {
	t.Helper()
	defer func() {
		// The native scenario owns its isolated Driver cluster. Only the
		// shared oracle needs database SQL teardown outside that lifetime.
		if !b.PG {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := c.ExecContext(ctx, "DROP DATABASE IF EXISTS stat_subscription_stats_peer"); err != nil {
			t.Error(err)
		}
	}()
	statSubscriptionExec(t, p, b, c, "CREATE DATABASE stat_subscription_stats_peer")
	pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "stat_subscription_stats_peer", "")
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
		if _, err := peer.ExecContext(ctx, "DROP SUBSCRIPTION IF EXISTS stat_sub_renamed"); err != nil {
			t.Error(err)
		}
	}()
	statSubscriptionCreate(t, p, pb, peer, "stat_sub_renamed")
	id := statSubscriptionID(t, p, peer, "stat_sub_renamed")
	for _, row := range local {
		if id == row[0] {
			t.Fatal("cross-database identity collision", id)
		}
	}
	want := append(append([][]any(nil), local...), statSubscriptionStatsRow(id, "stat_sub_renamed"))
	statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, want)
	statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, want)
	statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_renamed")
	remaining := [][]any{local[1], statSubscriptionStatsRow(id, "stat_sub_renamed")}
	statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, remaining)
	statSubscriptionStatsCheck(t, p, pb, peer, statSubscriptionStatsSQL, remaining)
}
