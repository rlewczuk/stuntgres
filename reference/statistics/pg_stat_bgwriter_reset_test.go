package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatBGWriterAbsentActivity(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := bgwriterGet(t, p, b, c)
		held := bgwriterHeld(t, p, c, bgwriterSQL)
		defer held.Statement.Close()
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE bgwriter_events(v integer)", 0),
			fixture.Exec("INSERT INTO bgwriter_events VALUES(1),(2)", 2),
			fixture.Exec("UPDATE bgwriter_events SET v=3 WHERE v=1", 1),
			fixture.Select("SELECT v FROM bgwriter_events ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}),
			fixture.Exec("DELETE FROM bgwriter_events", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			bgwriterSameReset(t, before, bgwriterGet(t, p, b, c))
			bgwriterSameReset(t, before, bgwriterGet(t, p, b, held))
		}
	})
}

func TestReferencePGStatBGWriterReset(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		const parameterSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		held := bgwriterHeld(t, p, c, parameterSQL)
		defer held.Statement.Close()
		before := bgwriterGet(t, p, b, c)
		for _, tc := range []struct {
			query string
			args  []any
		}{
			{"SELECT pg_catalog.pg_stat_reset_shared('bgwriter')", nil},
			{parameterSQL, []any{"bgwriter"}},
			{parameterSQL, []any{nil}},
			{"SELECT pg_catalog.pg_stat_reset_shared(NULL::text)", nil},
			{"SELECT pg_catalog.pg_stat_reset_shared()", nil},
		} {
			for _, prepared := range []bool{false, true} {
				lower := bgwriterClock(t, p, c)
				executor := fixture.Executor(c)
				var preparedCall fixture.HeldStatement
				if prepared {
					if tc.query == parameterSQL {
						executor = held
					} else {
						preparedCall = bgwriterHeld(t, p, c, tc.query)
						executor = preparedCall
					}
				}
				bgwriterVoid(t, p, b, executor, tc.query, "pg_stat_reset_shared", tc.args...)
				if preparedCall.Statement != nil {
					preparedCall.Statement.Close()
				}
				after := bgwriterGet(t, p, b, c)
				bgwriterEvent(t, before, after, lower, bgwriterClock(t, p, c))
				before = after
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(parameterSQL, 1, "bgwriter"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(parameterSQL, 1, "bgwriter"))
		before = bgwriterGet(t, p, b, c)
		for _, query := range []string{"SELECT pg_stat_reset_shared('wal')", "SELECT pg_stat_reset_shared('archiver')", "SELECT pg_stat_reset_slru(NULL)"} {
			if _, err := c.ExecContext(p.Ctx, query); err != nil {
				t.Fatal(query, err)
			}
			bgwriterSameReset(t, before, bgwriterGet(t, p, b, c))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_stat_reset_backend_stats(NULL)", []string{"pg_stat_reset_backend_stats"}, []string{"VOID"}, [][]any{{nil}}))
		bgwriterSameReset(t, before, bgwriterGet(t, p, b, c))
		want := bgwriterDiagnostic("22023", `unrecognized reset target: "WAL"`)
		want.Hint = `Target must be "archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", or "wal".`
		for _, executor := range []fixture.Executor{c, held} {
			step := fixture.ErrorDiagnostic(parameterSQL, want, "WAL")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			bgwriterSameReset(t, before, bgwriterGet(t, p, b, c))
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		lower := bgwriterClock(t, p, c)
		bgwriterVoid(t, p, b, held, parameterSQL, "pg_stat_reset_shared", "bgwriter")
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		bgwriterEvent(t, before, bgwriterGet(t, p, b, c), lower, bgwriterClock(t, p, c))
	})
}