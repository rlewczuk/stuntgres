package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionLifecycle(t *testing.T) {
	statSubscriptionScenario(t, []string{"stat_sub_a", "StatSubMixed", "stat_sub_renamed", "stat_sub_private", "stat_sub_enabled", "stat_sub_external"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statSubscriptionCreate(t, p, b, c, "stat_sub_a")
		statSubscriptionCreate(t, p, b, c, `"StatSubMixed"`)
		a, mixed := statSubscriptionID(t, p, c, "stat_sub_a"), statSubscriptionID(t, p, c, "StatSubMixed")
		if a == mixed {
			t.Fatal("distinct subscriptions share identity")
		}
		want := [][]any{statSubscriptionRow(a, "stat_sub_a"), statSubscriptionRow(mixed, "StatSubMixed")}
		held := statSubscriptionHeld(t, p, c, statSubscriptionSQL)
		defer held.Statement.Close()
		for _, e := range []fixture.Executor{c, held, held} {
			statSubscriptionCheck(t, p, b, e, statSubscriptionSQL, want)
		}
		for _, query := range []string{statSubscriptionSQL + " WHERE subname=$1", statSubscriptionSQL + " WHERE subid=$1::oid"} {
			filtered := statSubscriptionHeld(t, p, c, query)
			arg := any("stat_sub_a")
			if query == statSubscriptionSQL+" WHERE subid=$1::oid" {
				arg = a
			}
			for _, e := range []fixture.Executor{c, filtered, filtered} {
				statSubscriptionCheck(t, p, b, e, query, want[:1], arg)
			}
			filtered.Statement.Close()
		}
		projection := fixture.Select("SELECT s.subname AS name,s.subid,s.subid,s.pid FROM pg_stat_subscription s WHERE s.pid IS NULL ORDER BY s.subname", []string{"name", "subid", "subid", "pid"}, []string{"NAME", "OID", "OID", "INT4"}, [][]any{{"StatSubMixed", mixed, mixed, nil}, {"stat_sub_a", a, a, nil}})
		fixture.PgDescriptionExecutorRows(t, p, b, c, projection)
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(projection))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_subscription", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_a RENAME TO stat_sub_renamed")
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_renamed SET (binary=true,streaming=off)")
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_renamed SET PUBLICATION other,p WITH (refresh=false)")
		want[0] = statSubscriptionRow(a, "stat_sub_renamed")
		if got := statSubscriptionID(t, p, c, "stat_sub_renamed"); got != a {
			t.Fatal("rename reminted identity", got, a)
		}
		statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)

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
		privateWant := append(append([][]any(nil), want...), statSubscriptionRow(private, "stat_sub_private"))
		statSubscriptionCheck(t, p, b, tx, statSubscriptionSQL, privateWant)
		statSubscriptionCheck(t, p, b, peer, statSubscriptionSQL, want)
		statSubscriptionExec(t, p, b, tx, "SAVEPOINT status_change")
		statSubscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION stat_sub_renamed RENAME TO stat_sub_a")
		statSubscriptionExec(t, p, b, tx, `DROP SUBSCRIPTION "StatSubMixed"`)
		statSubscriptionCheck(t, p, b, tx, statSubscriptionSQL, [][]any{statSubscriptionRow(a, "stat_sub_a"), statSubscriptionRow(private, "stat_sub_private")})
		statSubscriptionCheck(t, p, b, peer, statSubscriptionSQL, want)
		statSubscriptionExec(t, p, b, tx, "ROLLBACK TO status_change")
		statSubscriptionCheck(t, p, b, tx, statSubscriptionSQL, privateWant)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)
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
		want = append(want, statSubscriptionRow(private, "stat_sub_private"))
		statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)
		statSubscriptionCheck(t, p, b, peer, statSubscriptionSQL, want)
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_private")
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_renamed")
		statSubscriptionCreate(t, p, b, c, "stat_sub_renamed")
		fresh := statSubscriptionID(t, p, c, "stat_sub_renamed")
		if fresh == a {
			t.Fatal("drop/recreate reused identity")
		}
		want = [][]any{statSubscriptionRow(fresh, "stat_sub_renamed"), statSubscriptionRow(mixed, "StatSubMixed")}
		statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)

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
		statSubscriptionCheck(t, p, b, tx, statSubscriptionSQL+" WHERE subid=$1::oid", [][]any{statSubscriptionRow(enabled, "stat_sub_enabled")}, enabled)
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
			statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)
			statSubscriptionSentinel(t, p, b, c)
		}
		statSubscriptionCrossDatabase(t, p, b, c, want)
	})
}

func statSubscriptionCrossDatabase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, local [][]any) {
	t.Helper()
	defer func() {
		// The native scenario owns its isolated Driver cluster. Only the
		// shared oracle needs database SQL teardown outside that lifetime.
		if !b.PG {
			return
		}
		if _, err := c.ExecContext(context.Background(), "DROP DATABASE IF EXISTS stat_subscription_peer"); err != nil {
			t.Error(err)
		}
	}()
	statSubscriptionExec(t, p, b, c, "CREATE DATABASE stat_subscription_peer")
	pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "stat_subscription_peer", "")
	defer pool.Close()
	peer, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	pb := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder, PG: b.PG}
	defer peer.ExecContext(context.Background(), "DROP SUBSCRIPTION IF EXISTS stat_sub_renamed")
	statSubscriptionCreate(t, p, pb, peer, "stat_sub_renamed")
	id := statSubscriptionID(t, p, peer, "stat_sub_renamed")
	for _, row := range local {
		if id == row[0] {
			t.Fatal("cross-database identity collision", id)
		}
	}
	want := append(append([][]any(nil), local...), statSubscriptionRow(id, "stat_sub_renamed"))
	statSubscriptionCheck(t, p, b, c, statSubscriptionSQL, want)
	statSubscriptionCheck(t, p, pb, peer, statSubscriptionSQL, want)
	statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_renamed")
	remaining := [][]any{local[1], statSubscriptionRow(id, "stat_sub_renamed")}
	statSubscriptionCheck(t, p, b, c, statSubscriptionSQL, remaining)
	statSubscriptionCheck(t, p, pb, peer, statSubscriptionSQL, remaining)
}
