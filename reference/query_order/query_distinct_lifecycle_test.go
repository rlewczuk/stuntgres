package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctLifecycle(t *testing.T) {
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE f(id INT)", 0))
		run(fixture.Exec("INSERT INTO f VALUES(1),(2)", 2))
		for _, q := range []string{"SELECT DISTINCT 1/(id-2) AS q FROM f LIMIT 1", "SELECT DISTINCT 0 AS k,1/(id-2) AS q FROM f LIMIT 1"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("runtime failure must prepare", err)
			}
			func() {
				defer stmt.Close()
				for _, held := range []bool{false, true} {
					var rows *sql.Rows
					if held {
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, q)
					}
					if rows != nil {
						if !b.PG {
							rows.Close()
							t.Fatal("native failure published rows")
						}
						if rows.Next() {
							rows.Close()
							t.Fatal("DISTINCT published a partial row")
						}
						err = rows.Err()
						if closeErr := rows.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
					}
					want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					fixture.AssertDiagnostic(t, err, &want)
				}
			}()
		}
		run(fixture.Exec("CREATE TABLE d(id INT)", 0))
		run(fixture.Exec("INSERT INTO d VALUES(17)", 1))
		s := fixture.ImplicitError("INSERT INTO d SELECT DISTINCT 1/(id-2) FROM f LIMIT 1", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.ErrorStage = "execute"
		fixture.QueryDistinctBoth(run, s)
		run(fixture.Select("SELECT id FROM d", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		q := "SELECT DISTINCT id FROM f ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				if held {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query exposed rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("context failure must remain ordinary", err)
				}
			}
		} else {
			// Observe genuine server admission and join cancellation. No invented
			// native pg_sleep or race-prone fast transport cancellation assertion.
			fixture.SessionCancel(t, p.Ctx, c, false, true)
		}
		s = fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
	})
}
