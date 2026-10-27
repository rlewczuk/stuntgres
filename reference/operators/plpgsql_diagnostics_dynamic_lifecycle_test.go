package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func diagnosticsDynamicLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "dynamic_direct_cancel"
		if prepared { name = "dynamic_held_cancel" }
		t.Run(name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE dg_dynamic_cancel_marks(i integer)",
					"CREATE FUNCTION dg_dynamic_cancel_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; RETURN s; END$$",
					"CREATE FUNCTION dg_dynamic_cancel(slow boolean) RETURNS text LANGUAGE plpgsql AS $$\nDECLARE n bigint; s text;\nBEGIN\n INSERT INTO dg_dynamic_cancel_marks VALUES(9);\n IF slow THEN EXECUTE 'SELECT pg_sleep(30)'; END IF;\n PERFORM 1 WHERE false;\n EXECUTE 'SELECT dg_dynamic_cancel_leaf()' INTO s;\n GET DIAGNOSTICS n=ROW_COUNT;\n RETURN n::text || ':' || found::text || '|' || s;\nEND$$")
				q := "SELECT dg_dynamic_cancel($1::boolean) AS v"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer held.Close()
				var executor fixture.Executor = c
				if prepared { executor = fixture.HeldStatement{Executor: c, Statement: held, SQL: q} }
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(30)\"\nPL/pgSQL function dg_dynamic_cancel(boolean) line 5 at EXECUTE", nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if prepared { rows, err = held.QueryContext(ctx, true) } else { rows, err = c.QueryContext(ctx, q, true) }
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				sqlExecutionCount(t, p, b, c, "dg_dynamic_cancel_marks", 0)
				for range 2 {
					s := sqlExecutionStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"1:false|PL/pgSQL function dg_dynamic_cancel_leaf() line 1 at GET DIAGNOSTICS\nSQL statement \"SELECT dg_dynamic_cancel_leaf()\"\nPL/pgSQL function dg_dynamic_cancel(boolean) line 7 at EXECUTE"}}, false)
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				}
				sqlExecutionCount(t, p, b, c, "dg_dynamic_cancel_marks", 2)
			})
		})
	}
}
