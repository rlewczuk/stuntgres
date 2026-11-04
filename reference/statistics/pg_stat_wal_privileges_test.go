package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"wal_reader", "wal_monitor", "wal_stats"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"CREATE ROLE wal_reader", "CREATE ROLE wal_monitor", "CREATE ROLE wal_stats", "GRANT pg_monitor TO wal_monitor", "GRANT pg_read_all_stats TO wal_stats"} {
			fixture.StatisticsExec(t, p, b, c, query, 0)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_stat_wal TO PUBLIC", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() TO PUBLIC", "REVOKE SELECT (stats_reset) ON pg_catalog.pg_stat_wal FROM wal_reader", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM wal_reader"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error("ACL cleanup", query, err)
				}
			}
		}()
		held := archiverHeld(t, p, c, walSQL)
		defer held.Statement.Close()
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		resetHeld := archiverHeld(t, p, c, resetSQL)
		defer resetHeld.Statement.Close()
		before := walGet(t, p, b, c)
		backingACL := func(allowed, defaultACL bool) {
			t.Helper()
			walRoutineMetadata(t, p, b, c)
			step := fixture.Select("SELECT has_function_privilege('wal_reader','pg_catalog.pg_stat_get_wal()','EXECUTE') AS allowed,proacl IS NULL AS default_acl FROM pg_proc WHERE oid=1136", []string{"allowed", "default_acl"}, []string{"BOOL", "BOOL"}, [][]any{{allowed, defaultACL}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		backingACL(true, true)
		denied := fixture.ErrorDiagnostic(resetSQL, archiverDiagnostic("42501", "permission denied for function pg_stat_reset_shared"), "wal")
		denied.SelectRows, denied.ErrorStage = true, "execute"
		for _, role := range []string{"wal_reader", "wal_monitor", "wal_stats"} {
			fixture.StatisticsExec(t, p, b, c, "SET ROLE "+role, 0)
			walSame(t, before, walGet(t, p, b, c))
			walSame(t, before, walGet(t, p, b, held))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		}
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) TO wal_reader", 0)
		lower := archiverClock(t, p, c)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE wal_reader", 0)
		archiverVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "wal")
		after := walGet(t, p, b, held)
		walEvent(t, before, after, lower, archiverClock(t, p, c))
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM wal_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE wal_reader", 0)
		fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)

		selectDenied := fixture.ErrorDiagnostic(walSQL, archiverDiagnostic("42501", "permission denied for view pg_stat_wal"))
		selectDenied.SelectRows, selectDenied.ErrorStage = true, "execute"
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_wal FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE wal_reader", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, selectDenied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT (stats_reset) ON pg_catalog.pg_stat_wal TO wal_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE wal_reader", 0)
		const columnSQL = "SELECT stats_reset FROM pg_catalog.pg_stat_wal"
		columnHeld := archiverHeld(t, p, c, columnSQL)
		defer columnHeld.Statement.Close()
		checkColumn := func(executor fixture.Executor) {
			t.Helper()
			rows, err := executor.QueryContext(p.Ctx, columnSQL)
			if err != nil {
				t.Fatal("column-only wal SELECT", err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "stats_reset" {
				t.Fatal("column-only wal name", columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].Name() != "stats_reset" || types[0].DatabaseTypeName() != "TIMESTAMPTZ" || types[0].ScanType() != reflect.TypeFor[time.Time]() {
				t.Fatal("column-only wal TIMESTAMPTZ metadata", types, err)
			}
			if !rows.Next() {
				t.Fatal("missing column-only wal singleton", rows.Err())
			}
			var at time.Time
			if err := rows.Scan(&at); err != nil {
				t.Fatal("column-only wal non-NULL timestamp", err)
			}
			if !at.Equal(after.reset) {
				t.Fatal("column-only wal reset instant", at, after.reset)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra column-only wal row", rows.Err())
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
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_wal TO PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() FROM PUBLIC", 0)
		backingACL(false, false)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE wal_reader", 0)
		backing := archiverDiagnostic("42501", "permission denied for function pg_stat_get_wal")
		for _, pair := range []struct {
			executor fixture.Executor
			query    string
		}{{c, walSQL}, {held, walSQL}, {c, columnSQL}, {columnHeld, columnSQL}} {
			step := fixture.ErrorDiagnostic(pair.query, backing)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, pair.executor, b.Recorder, step)
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() TO PUBLIC", 0)
		backingACL(true, false)

		// Peer catalog invalidations are portable at the next transaction
		// boundary, independently of the statistics Reader's retained cut.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE wal_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		walSame(t, after, walGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, peer, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		backingACL(false, false)
		for _, pair := range []struct {
			executor fixture.Executor
			query    string
		}{{c, walSQL}, {held, walSQL}, {c, columnSQL}, {columnHeld, columnSQL}} {
			step := fixture.ErrorDiagnostic(pair.query, backing)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, pair.executor, b.Recorder, step)
		}
		step := fixture.ErrorDiagnostic(walSQL, backing)
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
		fixture.StatisticsExec(t, p, b, peer, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() TO PUBLIC", 0)
		backingACL(true, false)
		walSame(t, after, walGet(t, p, b, c))
		walSame(t, after, walGet(t, p, b, held))
		for _, executor := range []fixture.Executor{c, columnHeld} {
			checkColumn(executor)
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
	})
}
