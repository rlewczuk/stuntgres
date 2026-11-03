package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatArchiverPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"archiver_reader", "archiver_monitor", "archiver_stats"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"CREATE ROLE archiver_reader", "CREATE ROLE archiver_monitor", "CREATE ROLE archiver_stats", "GRANT pg_monitor TO archiver_monitor", "GRANT pg_read_all_stats TO archiver_stats"} {
			fixture.StatisticsExec(t, p, b, c, query, 0)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_stat_archiver TO PUBLIC", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() TO PUBLIC", "REVOKE SELECT (stats_reset) ON pg_catalog.pg_stat_archiver FROM archiver_reader", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM archiver_reader"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error("ACL cleanup", query, err)
				}
			}
		}()
		held := archiverHeld(t, p, c, archiverSQL)
		defer held.Statement.Close()
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		resetHeld := archiverHeld(t, p, c, resetSQL)
		defer resetHeld.Statement.Close()
		before := archiverGet(t, p, b, c)
		backingACL := func(allowed, defaultACL bool) {
			t.Helper()
			archiverRoutineMetadata(t, p, b, c)
			step := fixture.Select("SELECT has_function_privilege('archiver_reader','pg_catalog.pg_stat_get_archiver()','EXECUTE') AS allowed,proacl IS NULL AS default_acl FROM pg_proc WHERE oid=3195", []string{"allowed", "default_acl"}, []string{"BOOL", "BOOL"}, [][]any{{allowed, defaultACL}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		backingACL(true, true)
		denied := fixture.ErrorDiagnostic(resetSQL, archiverDiagnostic("42501", "permission denied for function pg_stat_reset_shared"), "archiver")
		denied.SelectRows, denied.ErrorStage = true, "execute"
		for _, role := range []string{"archiver_reader", "archiver_monitor", "archiver_stats"} {
			fixture.StatisticsExec(t, p, b, c, "SET ROLE "+role, 0)
			archiverSame(t, before, archiverGet(t, p, b, c))
			archiverSame(t, before, archiverGet(t, p, b, held))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		}
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) TO archiver_reader", 0)
		lower := archiverClock(t, p, c)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE archiver_reader", 0)
		archiverVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "archiver")
		after := archiverGet(t, p, b, held)
		archiverEvent(t, before, after, lower, archiverClock(t, p, c))
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM archiver_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE archiver_reader", 0)
		fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)

		selectDenied := fixture.ErrorDiagnostic(archiverSQL, archiverDiagnostic("42501", "permission denied for view pg_stat_archiver"))
		selectDenied.SelectRows, selectDenied.ErrorStage = true, "execute"
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_archiver FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE archiver_reader", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, selectDenied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT (stats_reset) ON pg_catalog.pg_stat_archiver TO archiver_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE archiver_reader", 0)
		const columnSQL = "SELECT stats_reset FROM pg_catalog.pg_stat_archiver"
		columnHeld := archiverHeld(t, p, c, columnSQL)
		defer columnHeld.Statement.Close()
		checkColumn := func(executor fixture.Executor) {
			t.Helper()
			rows, err := executor.QueryContext(p.Ctx, columnSQL)
			if err != nil {
				t.Fatal("column-only archiver SELECT", err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "stats_reset" {
				t.Fatal("column-only archiver name", columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].Name() != "stats_reset" || types[0].DatabaseTypeName() != "TIMESTAMPTZ" || types[0].ScanType() != reflect.TypeFor[time.Time]() {
				t.Fatal("column-only archiver TIMESTAMPTZ metadata", types, err)
			}
			if !rows.Next() {
				t.Fatal("missing column-only archiver singleton", rows.Err())
			}
			var at time.Time
			if err := rows.Scan(&at); err != nil {
				t.Fatal("column-only archiver non-NULL timestamp", err)
			}
			if !at.Equal(after.reset) {
				t.Fatal("column-only archiver reset instant", at, after.reset)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra column-only archiver row", rows.Err())
			}
		}
		for _, executor := range []fixture.Executor{c, columnHeld} {
			checkColumn(executor)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, c, "SAVEPOINT denied_select", 0)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT denied_select", 0)
		if at := archiverSnapshotTime(t, p, c); at.Valid {
			t.Fatal("denied view SELECT captured snapshot", at)
		}
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_archiver TO PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() FROM PUBLIC", 0)
		backingACL(false, false)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE archiver_reader", 0)
		backing := archiverDiagnostic("42501", "permission denied for function pg_stat_get_archiver")
		for _, pair := range []struct {
			executor fixture.Executor
			query    string
		}{{c, archiverSQL}, {held, archiverSQL}, {c, columnSQL}, {columnHeld, columnSQL}} {
			step := fixture.ErrorDiagnostic(pair.query, backing)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, pair.executor, b.Recorder, step)
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() TO PUBLIC", 0)
		backingACL(true, false)

		// Peer catalog invalidations are portable at the next transaction
		// boundary, independently of the statistics Reader's retained cut.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE archiver_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		archiverSame(t, after, archiverGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, peer, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		backingACL(false, false)
		for _, pair := range []struct {
			executor fixture.Executor
			query    string
		}{{c, archiverSQL}, {held, archiverSQL}, {c, columnSQL}, {columnHeld, columnSQL}} {
			step := fixture.ErrorDiagnostic(pair.query, backing)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, pair.executor, b.Recorder, step)
		}
		step := fixture.ErrorDiagnostic(archiverSQL, backing)
		step.SelectRows, step.ErrorStage = true, "execute"
		// A denial before first cumulative access must not initialize a cut.
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, c, "SAVEPOINT denied_read", 0)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT denied_read", 0)
		if at := archiverSnapshotTime(t, p, c); at.Valid {
			t.Fatal("denied read captured snapshot", at)
		}
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		fixture.StatisticsExec(t, p, b, peer, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() TO PUBLIC", 0)
		backingACL(true, false)
		archiverSame(t, after, archiverGet(t, p, b, c))
		archiverSame(t, after, archiverGet(t, p, b, held))
		for _, executor := range []fixture.Executor{c, columnHeld} {
			checkColumn(executor)
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
	})
}
