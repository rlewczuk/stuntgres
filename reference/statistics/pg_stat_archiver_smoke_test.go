package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatArchiverFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		a := archiverGet(t, p, b, c)
		held := archiverHeld(t, p, c, archiverSQL)
		defer held.Statement.Close()
		archiverSame(t, a, archiverGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE archiver_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO archiver_sentinel VALUES(37)", 1)
		archiverSentinel(t, p, b, c)
		archiverSame(t, a, archiverGet(t, p, b, held))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		reset := archiverHeld(t, p, peer, resetSQL)
		defer reset.Statement.Close()
		old, err := held.QueryContext(p.Ctx, archiverSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		lower := archiverClock(t, p, peer)
		archiverVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "archiver")
		archiverSame(t, a, archiverRead(t, old, b.PG))
		current := archiverGet(t, p, b, held)
		archiverEvent(t, a, current, lower, archiverClock(t, p, peer))
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		a = archiverGet(t, p, b, held)
		lower = archiverClock(t, p, peer)
		archiverVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "archiver")
		current = archiverGet(t, p, b, peer)
		archiverEvent(t, a, current, lower, archiverClock(t, p, peer))
		if !current.reset.After(a.reset) {
			t.Fatal("reset did not distinguish snapshot cut")
		}
		archiverSame(t, a, archiverGet(t, p, b, held))
		fixture.StatisticsClearSnapshot(t, p, b, c)
		archiverSame(t, current, archiverGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_archiver SET archived_count=1 WHERE FALSE", archiverMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		archiverSame(t, current, archiverGet(t, p, b, held))
		archiverSentinel(t, p, b, c)
	})
}
