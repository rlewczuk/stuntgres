package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAutoExplainLifecycle(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ae_lifecycle(id integer)", 0))
		run(fixture.Exec("INSERT INTO ae_lifecycle VALUES(1),(2)", 2))
		autoExplainConfigure(t, p, b, c, notices)
		failure := fixture.ErrorDiagnostic("SELECT 10/(id-id) FROM ae_lifecycle", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		run(failure)
		autoExplainQuiet(t, notices)
		// Both routes wait for an actual executing owner before cancellation.
		if b.PG {
			fixture.SessionCancelQuery(t, p.Ctx, c, true, "SELECT pg_sleep(600)", true)
			autoExplainJSON(t, notices, "SELECT pg_backend_pid()", nil, true, 1)
		} else {
			statement, err := c.PrepareContext(p.Ctx, "SELECT * FROM jsonb_each($1::jsonb)")
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			pause := stuntgres.NewExecutionPause("json.each.next")
			defer pause.Resume()
			ctx, cancel := context.WithCancel(stuntgres.WithExecutionPause(p.Ctx, pause))
			defer cancel()
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				pause.Resume()
				if !joined {
					<-done
				}
			}()
			go func() {
				rows, err := statement.QueryContext(ctx, `{"one":1,"two":2}`)
				if rows != nil {
					for rows.Next() {
					}
					if err == nil {
						err = rows.Err()
					}
					if closeErr := rows.Close(); err == nil {
						err = closeErr
					}
				}
				done <- err
			}()
			if err := pause.Wait(p.Ctx); err != nil {
				t.Fatal("executor never began", err)
			}
			cancel()
			err = <-done
			joined = true
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary cancellation lost", err)
			}
		}
		autoExplainQuiet(t, notices)
		const query = "SELECT id FROM ae_lifecycle ORDER BY id"
		run(fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		autoExplainJSON(t, notices, query, nil, true, 2)
		// Transport disposal of an already complete detached execution is not a
		// second executor end. PostgreSQL likewise ends this fully read query once.
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value int64
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		autoExplainJSON(t, notices, query, nil, true, 2)
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		autoExplainQuiet(t, notices)
	})
}
