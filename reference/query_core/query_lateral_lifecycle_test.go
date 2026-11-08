package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLateralLifecycle(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		lateralFixture(run)
		run(fixture.Exec("CREATE TABLE lat_sink(n INT)", 0))
		run(fixture.Exec("INSERT INTO lat_sink VALUES(99)", 1))
		// OFFSET fences the ordered left source: one invocation succeeds before
		// the next child fails. No native cursor or DML prefix may be published.
		const failing = "SELECT x.n FROM (SELECT id FROM lat_t ORDER BY id OFFSET 0) t CROSS JOIN LATERAL (SELECT 10/(t.id-2) AS n) x"
		want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		for _, prepared := range []bool{false, true} {
			var x fixture.Executor = c
			if prepared {
				stmt, err := c.PrepareContext(p.Ctx, failing)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: failing}
			}
			subqueryQueryFailure(t, p, b, x, failing, want)
			for _, q := range []string{
				"INSERT INTO lat_sink " + failing,
				"CREATE TABLE lat_failed AS " + failing,
				"WITH bad AS (" + failing + ") INSERT INTO lat_sink SELECT n FROM bad",
			} {
				s := fixture.ErrorDiagnostic(q, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				subqueryBoth(run, fixture.Select("SELECT n FROM lat_sink", []string{"n"}, []string{"INT4"}, [][]any{{int64(99)}}))
				const missing = "SELECT * FROM lat_failed"
				subqueryAnalysisFailure(run, missing, lateralDiagnostic(missing, "lat_failed", "42P01", `relation "lat_failed" does not exist`, "", ""))
			}
			subqueryBoth(run, lateralLatestStep())
		}
		stmt, err := c.PrepareContext(p.Ctx, lateralHeldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: lateralHeldSQL}
		// A real advisory-lock grant/wait supplies deterministic SQL admission
		// on BOTH backends; the helper cancels, joins and proves lock cleanup.
		const gated = "SELECT t.id FROM lat_t t CROSS JOIN LATERAL (SELECT pg_advisory_xact_lock((t.id*0+$1)::bigint) AS locked) x WHERE x.locked IS NOT NULL ORDER BY t.id"
		for _, prepared := range []bool{false, true} {
			var x fixture.Executor = c
			if prepared {
				gateStmt, err := c.PrepareContext(p.Ctx, gated)
				if err != nil {
					t.Fatal(err)
				}
				defer gateStmt.Close()
				x = fixture.HeldStatement{Executor: c, Statement: gateStmt, SQL: gated}
			}
			for _, query := range []bool{false, true} {
				fixture.OutputGateCancel(t, p, b, c, false, func(ctx context.Context) fixture.RelationOutcome {
					var out fixture.RelationOutcome
					if query {
						out.Rows, out.Err = x.QueryContext(ctx, gated, int64(67108864))
					} else {
						out.Result, out.Err = x.ExecContext(ctx, gated, int64(67108864))
					}
					return out
				})
				// Reuse the very statement that failed, with an unblocked key.
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(gated, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, int64(2)))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(lateralHeldSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(103)}}, int64(100), int64(2)))
			}
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := held.QueryContext(ctx, lateralHeldSQL, int64(100), int64(2))
			if rows != nil {
				rows.Close()
				t.Fatal("pre-canceled lateral query published a cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			result, err := held.ExecContext(ctx, lateralHeldSQL, int64(100), int64(2))
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("pre-canceled lateral result", result, err)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(lateralHeldSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(103)}}, int64(100), int64(2)))
	})
}
