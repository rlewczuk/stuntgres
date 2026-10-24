package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoTablesDiagnostics(t *testing.T) {
	t.Run("missing_projection", func(t *testing.T) {
		itScenario(t, func(c *itCase) {
			for _, prepared := range []bool{false, true} {
				d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
				d.Position = "8"
				s := fixture.ErrorDiagnostic("SELECT absent FROM information_schema.tables", d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				c.run(c.conn, s)
			}
		})
	})
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			itScenario(t, func(c *itCase) {
				c.exec("CREATE TABLE it_scope.sentinel(a integer)")
				c.run(c.conn, fixture.Exec("INSERT INTO it_scope.sentinel VALUES(7)", 1))
				q := map[string]string{"INSERT": "INSERT INTO information_schema.tables(table_name) VALUES('fake')", "UPDATE": "UPDATE information_schema.tables SET table_name='fake' WHERE table_schema='it_scope' AND table_name='sentinel'", "DELETE": "DELETE FROM information_schema.tables WHERE table_schema='it_scope' AND table_name='sentinel'"}[event]
				primary := map[string]string{"INSERT": `cannot insert into view "tables"`, "UPDATE": `cannot update view "tables"`, "DELETE": `cannot delete from view "tables"`}[event]
				d := fixture.ControlsReferenceDiagnostic("55000", primary)
				d.Detail = "Views that do not select from a single table or view are not automatically updatable."
				verb := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[event]
				d.Hint = "To enable " + verb + " the view, provide an INSTEAD OF " + event + " trigger or an unconditional ON " + event + " DO INSTEAD rule."
				for _, prepared := range []bool{false, true} {
					// Release-source expectations; root discovery resolves tables-specific
					// optional fields and the protocol Prepare/execute boundary.
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
					c.bag(c.row("sentinel", "BASE TABLE", "YES"))
					c.check("fake")
					c.run(c.conn, fixture.Select("SELECT a FROM it_scope.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
		})
	}
	t.Run("ordinary_cancellation", func(t *testing.T) {
		itScenario(t, func(c *itCase) {
			c.exec("CREATE TABLE it_scope.cancel(a integer)")
			c.check("cancel", c.row("cancel", "BASE TABLE", "YES"))
			if c.b.PG {
				return
			} // pq pre-admission cancellation is transport-specific.
			q := strings.ReplaceAll(itSelect, "$1", "'it_scope'")
			q = strings.ReplaceAll(q, "$2", "'cancel'")
			held := c.hold(q)
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, x := range []fixture.Executor{c.conn, held} {
				rows, err := x.QueryContext(ctx, q)
				if rows != nil {
					rows.Close()
					c.t.Fatal("canceled metadata published rows")
				}
				if !errors.Is(err, context.Canceled) {
					c.t.Fatalf("cancellation=%v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatalf("ordinary cancellation SQLSTATE=%s", state.SQLState())
				}
			}
			c.check("cancel", c.row("cancel", "BASE TABLE", "YES"))
		})
	})
}
