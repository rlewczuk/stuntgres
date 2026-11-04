package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALReset(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		const parameterSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		held := archiverHeld(t, p, c, parameterSQL)
		defer held.Statement.Close()
		before := walGet(t, p, b, c)
		view := archiverHeld(t, p, c, walSQL)
		defer view.Statement.Close()
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE wal_events(v integer)", 0),
			fixture.Exec("INSERT INTO wal_events VALUES(1),(2)", 2),
			fixture.Exec("UPDATE wal_events SET v=3 WHERE v=1", 1),
			fixture.Exec("DELETE FROM wal_events", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			walSame(t, before, walGet(t, p, b, c))
			walSame(t, before, walGet(t, p, b, view))
		}
		for _, tc := range []struct {
			query string
			args  []any
		}{
			{"SELECT pg_catalog.pg_stat_reset_shared('wal')", nil},
			{parameterSQL, []any{"wal"}},
			{parameterSQL, []any{nil}},
			{"SELECT pg_catalog.pg_stat_reset_shared(NULL)", nil},
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
				after := walGet(t, p, b, c)
				walEvent(t, before, after, lower, archiverClock(t, p, c))
				before = after
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(parameterSQL, 1, "wal"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(parameterSQL, 1, "wal"))
		before = walGet(t, p, b, c)
		for _, query := range []string{"SELECT pg_stat_reset_shared('archiver')", "SELECT pg_stat_reset_shared('bgwriter')", "SELECT pg_stat_reset_slru(NULL)"} {
			if _, err := c.ExecContext(p.Ctx, query); err != nil {
				t.Fatal(query, err)
			}
			walSame(t, before, walGet(t, p, b, c))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_stat_reset_backend_stats(NULL)", []string{"pg_stat_reset_backend_stats"}, []string{"VOID"}, [][]any{{nil}}))
		walSame(t, before, walGet(t, p, b, c))
		want := archiverDiagnostic("22023", `unrecognized reset target: "WAL"`)
		want.Hint = `Target must be "archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", or "wal".`
		for _, executor := range []fixture.Executor{c, held} {
			step := fixture.ErrorDiagnostic(parameterSQL, want, "WAL")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			walSame(t, before, walGet(t, p, b, c))
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		lower := archiverClock(t, p, c)
		archiverVoid(t, p, b, held, parameterSQL, "pg_stat_reset_shared", "wal")
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		walEvent(t, before, walGet(t, p, b, c), lower, archiverClock(t, p, c))
		if !b.PG {
			current := walGet(t, p, b, c)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, parameterSQL, "wal")
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled reset returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary reset cancellation", err)
				}
				walSame(t, current, walGet(t, p, b, c))
			}
		}
	})
}
