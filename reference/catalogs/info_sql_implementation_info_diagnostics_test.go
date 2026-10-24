package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSQLImplementationInfoDiagnostics(t *testing.T) {
	siiScenario(t, true, func(c *siiCase) {
		want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		want.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM "+siiTable, want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(c.conn, s)
		}
		held := c.hold(siiQuery)
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(ctx, siiQuery)
				if rows != nil {
					rows.Close()
					c.t.Fatal("canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					c.t.Fatalf("canceled query: %v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatalf("ordinary cancellation has SQLSTATE %s", state.SQLState())
				}
			}
		}
		c.check(held, siiQuery, c.want)
		c.exec("CREATE ROLE sii_reader")
		// Catalog-source ACL SQL is outside native admission. Real native ACL
		// replacement and retained authorization are covered by engine units.
		if c.b.PG {
			c.exec("BEGIN")
			full := c.hold(siiQuery)
			emptyQuery := siiQuery + " WHERE FALSE"
			empty := c.hold(emptyQuery)
			c.run(full, c.projection(siiQuery, c.want))
			c.run(empty, c.projection(emptyQuery, nil))
			c.exec("REVOKE SELECT ON information_schema.sql_implementation_info FROM PUBLIC", "SET LOCAL ROLE sii_reader")
			for _, target := range []struct {
				q    string
				held fixture.HeldStatement
			}{{siiQuery, full}, {emptyQuery, empty}} {
				for _, route := range []string{"direct", "prepared", "held"} {
					c.exec("SAVEPOINT sii_denial")
					s := fixture.ErrorDiagnostic(target.q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sql_implementation_info"))
					s.SelectRows, s.ErrorStage = true, "execute"
					var executor fixture.Executor = c.conn
					if route == "prepared" {
						s.Prepared = true
					}
					if route == "held" {
						executor = target.held
					}
					c.run(executor, s)
					c.exec("ROLLBACK TO SAVEPOINT sii_denial", "RELEASE SAVEPOINT sii_denial")
				}
			}
			c.exec("ROLLBACK")
		}
		c.exec("SET SESSION AUTHORIZATION sii_reader")
		c.check(c.conn, siiQuery, c.want)
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
