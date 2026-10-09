package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceQueryDistinctLifecycle(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE f(id INT)", 0))
		run(referenceExec("INSERT INTO f VALUES(1),(2)", 2))
		for _, q := range []string{"SELECT DISTINCT 1/(id-2) AS q FROM f LIMIT 1", "SELECT DISTINCT 0 AS k,1/(id-2) AS q FROM f LIMIT 1"} {
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal("runtime failure must prepare", err)
			}
			func() {
				defer stmt.Close()
				for _, held := range []bool{false, true} {
					var rows *sql.Rows
					if held {
						rows, err = stmt.QueryContext(p.ctx)
					} else {
						rows, err = c.QueryContext(p.ctx, q)
					}
					if rows != nil {
						if !b.pg {
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
					want := controlsReferenceDiagnostic("22012", "division by zero")
					referenceAssertDiagnostic(t, err, &want)
				}
			}()
		}
		run(referenceExec("CREATE TABLE d(id INT)", 0))
		run(referenceExec("INSERT INTO d VALUES(17)", 1))
		s := referenceImplicitError("INSERT INTO d SELECT DISTINCT 1/(id-2) FROM f LIMIT 1", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.errorStage = "execute"
		queryDistinctBoth(run, s)
		run(referenceSelect("SELECT id FROM d", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		q := "SELECT DISTINCT id FROM f ORDER BY id"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if !b.pg {
			ctx, cancel := context.WithCancel(p.ctx)
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
			referenceSessionCancel(t, p.ctx, c, false, true)
		}
		s = referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
	})
}
