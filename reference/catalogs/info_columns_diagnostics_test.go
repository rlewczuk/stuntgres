package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnsDiagnostics(t *testing.T) {
	t.Run("missing_projection", func(t *testing.T) {
		icScenario(t, func(c *icCase) {
			d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT absent FROM information_schema.columns", d)
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
			icScenario(t, func(c *icCase) {
				c.exec("CREATE TABLE ic_scope.sentinel(a integer DEFAULT 7)")
				c.run(c.conn, fixture.Exec("INSERT INTO ic_scope.sentinel VALUES(3)", 1))
				want := icPatch(c.integer("sentinel", "a", 1), map[string]any{"column_default": "7"})
				query := map[string]string{"INSERT": "INSERT INTO information_schema.columns(table_name) VALUES('fake')", "UPDATE": "UPDATE information_schema.columns SET table_name='fake' WHERE table_name='sentinel'", "DELETE": "DELETE FROM information_schema.columns WHERE table_name='sentinel'"}[event]
				// All logical diagnostic fields and direct/prepared stages are
				// checked, including the absence of other optional fields.
				d := fixture.ControlsReferenceDiagnostic("55000", "cannot "+strings.ToLower(event)+" "+map[string]string{"INSERT": "into", "UPDATE": "view", "DELETE": "from"}[event]+" "+map[string]string{"INSERT": "view ", "UPDATE": "", "DELETE": "view "}[event]+`"columns"`)
				d.Detail = "Views that do not select from a single table or view are not automatically updatable."
				verb := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[event]
				d.Hint = "To enable " + verb + " the view, provide an INSTEAD OF " + event + " trigger or an unconditional ON " + event + " DO INSTEAD rule."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
					c.check("sentinel", want)
					c.run(c.conn, fixture.Select("SELECT * FROM ic_scope.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(3)}}))
					c.check("fake")
				}
			})
		})
	}
	t.Run("failed_alter", func(t *testing.T) {
		icScenario(t, func(c *icCase) {
			c.exec("CREATE TABLE ic_scope.bad(a integer)")
			c.run(c.conn, fixture.Exec("INSERT INTO ic_scope.bad VALUES(NULL)", 1))
			d := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "bad" contains null values`)
			d.Schema, d.Table, d.Column = "ic_scope", "bad", "a"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("ALTER TABLE ic_scope.bad ALTER COLUMN a SET NOT NULL", d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(c.conn, s)
				c.check("bad", c.integer("bad", "a", 1))
				c.run(c.conn, fixture.Select("SELECT * FROM ic_scope.bad", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
			}
		})
	})
	t.Run("ordinary_cancellation", func(t *testing.T) {
		icScenario(t, func(c *icCase) {
			t := c.t
			c.exec("CREATE TABLE ic_scope.cancel(a integer)")
			s := icProjection(c.b.PG, "table_schema='ic_scope' AND table_name='cancel'", [][]any{c.integer("cancel", "a", 1)})
			c.run(c.conn, s)
			if c.b.PG {
				return
			} // pq pre-admission cancellation is transport-specific.
			held := c.hold(s.Query)
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, x := range []fixture.Executor{c.conn, held} {
				rows, err := x.QueryContext(ctx, s.Query)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled metadata published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
				}
			}
			c.run(c.conn, s)
		})
	})
}
