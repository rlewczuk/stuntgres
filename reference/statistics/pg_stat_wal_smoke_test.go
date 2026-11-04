package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		a := walGet(t, p, b, c)
		held := archiverHeld(t, p, c, walSQL)
		defer held.Statement.Close()
		walSame(t, a, walGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE wal_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO wal_sentinel VALUES(37)", 1)
		walSentinel(t, p, b, c)
		walSame(t, a, walGet(t, p, b, held))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		reset := archiverHeld(t, p, peer, resetSQL)
		defer reset.Statement.Close()
		old, err := held.QueryContext(p.Ctx, walSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		lower := archiverClock(t, p, peer)
		archiverVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "wal")
		walSame(t, a, walRead(t, old, b.PG))
		current := walGet(t, p, b, held)
		walEvent(t, a, current, lower, archiverClock(t, p, peer))
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		a = walGet(t, p, b, held)
		lower = archiverClock(t, p, peer)
		archiverVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "wal")
		current = walGet(t, p, b, peer)
		walEvent(t, a, current, lower, archiverClock(t, p, peer))
		if !current.reset.After(a.reset) {
			t.Fatal("reset did not distinguish snapshot cut")
		}
		walSame(t, a, walGet(t, p, b, held))
		fixture.StatisticsClearSnapshot(t, p, b, c)
		walSame(t, current, walGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_wal SET wal_records=1 WHERE FALSE", walMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		walSame(t, current, walGet(t, p, b, held))
		walSentinel(t, p, b, c)
	})
}
