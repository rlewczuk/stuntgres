package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoAttributesDiagnostics(t *testing.T) {
	t.Run("missing_projection", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			for _, prefix := range []string{"", "/*é*/ "} {
				d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
				d.Position = "8"
				if prefix != "" {
					d.Position = "14"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(prefix+"SELECT missing FROM information_schema.attributes", d)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(s)
				}
			}
		})
	})
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			attrScenario(t, func(c *attrCase) {
				c.exec("CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer)", "CREATE TABLE attr_scope.sentinel(v integer)")
				c.run(fixture.Exec("INSERT INTO attr_scope.sentinel VALUES(3)", 1))
				where := "udt_schema='attr_scope' AND udt_name='pair'"
				h := c.hold(where)
				query := map[string]string{
					"INSERT": "INSERT INTO information_schema.attributes(udt_name) VALUES('fake')",
					"UPDATE": "UPDATE information_schema.attributes SET udt_name='fake' WHERE udt_name='pair'",
					"DELETE": "DELETE FROM information_schema.attributes WHERE udt_name='pair'",
				}[event]
				action := map[string]string{"INSERT": "insert into", "UPDATE": "update", "DELETE": "delete from"}[event]
				d := fixture.ControlsReferenceDiagnostic("55000", "cannot "+action+` view "attributes"`)
				d.Detail = "Views that do not select from a single table or view are not automatically updatable."
				verb := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[event]
				d.Hint = "To enable " + verb + " the view, provide an INSTEAD OF " + event + " trigger or an unconditional ON " + event + " DO INSTEAD rule."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(s)
					c.held(h, where, nil, c.integer("pair", "id", 1))
					c.check("udt_schema='attr_scope' AND udt_name='fake'")
					c.run(fixture.Select("SELECT v FROM attr_scope.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
				}
			})
		})
	}
	t.Run("source_select", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE ROLE attr_reader", "CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer)")
			where := "udt_schema='attr_scope' AND FALSE"
			h := c.hold(where)
			c.t.Cleanup(func() {
				c.exec("RESET SESSION AUTHORIZATION", "GRANT SELECT ON information_schema.attributes TO PUBLIC")
			})
			c.exec("REVOKE SELECT ON information_schema.attributes FROM PUBLIC", "SET SESSION AUTHORIZATION attr_reader")
			c.run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"attr_reader", "attr_reader"}}))
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view attributes")
			s := fixture.ErrorDiagnostic(attrProjection(c.b.PG, where, nil).Query, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			c.run(s)
			c.run(fixture.Prepared(s))
			fixture.RunStep(c.t, c.p.Ctx, h, c.r, s)
			c.exec("RESET SESSION AUTHORIZATION", "GRANT SELECT ON information_schema.attributes TO PUBLIC", "SET SESSION AUTHORIZATION attr_reader")
			c.held(h, where, nil)
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("ordinary_cancellation", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer)")
			where := "udt_schema='attr_scope' AND udt_name='pair'"
			s := attrProjection(c.b.PG, where, [][]any{c.integer("pair", "id", 1)})
			c.run(s)
			if c.b.PG {
				return // pq pre-admission cancellation is transport-specific.
			}
			h := c.hold(where)
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, x := range []fixture.Executor{c.c, h} {
				rows, err := x.QueryContext(ctx, s.Query)
				if rows != nil {
					rows.Close()
					c.t.Fatal("canceled metadata published rows")
				}
				if !errors.Is(err, context.Canceled) {
					c.t.Fatalf("cancel=%v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
				}
			}
			c.held(h, where, nil, c.integer("pair", "id", 1))
		})
	})
}
