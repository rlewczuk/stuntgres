package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatArchiverEvents(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := archiverGet(t, p, b, c)
		held := archiverHeld(t, p, c, archiverSQL)
		defer held.Statement.Close()
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE archiver_events(v integer)", 0),
			fixture.Exec("INSERT INTO archiver_events VALUES(1),(2)", 2),
			fixture.Exec("UPDATE archiver_events SET v=3 WHERE v=1", 1),
			fixture.Select("SELECT v FROM archiver_events ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}),
			fixture.Exec("DELETE FROM archiver_events", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			archiverSame(t, before, archiverGet(t, p, b, c))
			archiverSame(t, before, archiverGet(t, p, b, held))
		}
	})
}

func TestReferencePGStatArchiverReset(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		const parameterSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		held := archiverHeld(t, p, c, parameterSQL)
		defer held.Statement.Close()
		before := archiverGet(t, p, b, c)
		for _, tc := range []struct {
			query string
			args  []any
		}{
			{"SELECT pg_catalog.pg_stat_reset_shared('archiver')", nil},
			{parameterSQL, []any{"archiver"}},
			{parameterSQL, []any{nil}},
			{"SELECT pg_catalog.pg_stat_reset_shared()", nil},
		} {
			for _, prepared := range []bool{false, true} {
				lower := archiverClock(t, p, c)
				executor := fixture.Executor(c)
				var preparedCall fixture.HeldStatement
				if prepared {
					if tc.query == parameterSQL {
						executor = held
					} else {
						preparedCall = archiverHeld(t, p, c, tc.query)
						executor = preparedCall
					}
				}
				archiverVoid(t, p, b, executor, tc.query, "pg_stat_reset_shared", tc.args...)
				if preparedCall.Statement != nil {
					preparedCall.Statement.Close()
				}
				after := archiverGet(t, p, b, c)
				archiverEvent(t, before, after, lower, archiverClock(t, p, c))
				before = after
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(parameterSQL, 1, "archiver"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(parameterSQL, 1, "archiver"))
		before = archiverGet(t, p, b, c)
		for _, query := range []string{"SELECT pg_stat_reset_shared('wal')", "SELECT pg_stat_reset_shared('bgwriter')", "SELECT pg_stat_reset_slru(NULL)"} {
			if _, err := c.ExecContext(p.Ctx, query); err != nil {
				t.Fatal(query, err)
			}
			archiverSame(t, before, archiverGet(t, p, b, c))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_stat_reset_backend_stats(NULL)", []string{"pg_stat_reset_backend_stats"}, []string{"VOID"}, [][]any{{nil}}))
		archiverSame(t, before, archiverGet(t, p, b, c))
		want := archiverDiagnostic("22023", `unrecognized reset target: "WAL"`)
		want.Hint = `Target must be "archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", or "wal".`
		for _, executor := range []fixture.Executor{c, held} {
			step := fixture.ErrorDiagnostic(parameterSQL, want, "WAL")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			archiverSame(t, before, archiverGet(t, p, b, c))
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		lower := archiverClock(t, p, c)
		archiverVoid(t, p, b, held, parameterSQL, "pg_stat_reset_shared", "archiver")
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		archiverEvent(t, before, archiverGet(t, p, b, c), lower, archiverClock(t, p, c))
	})
}
