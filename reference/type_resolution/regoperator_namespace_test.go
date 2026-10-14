package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegOperatorNamespaceAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regop_owner_a", "regop_owner_b"})
	fixture.SchemaReferenceCleanup(t, "regop_private")
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, q := range []string{"CREATE ROLE regop_owner_a", "CREATE ROLE regop_owner_b", "CREATE SCHEMA regop_private AUTHORIZATION regop_owner_a", "CREATE DOMAIN regop_private.secret AS integer", "SET search_path TO regop_private,public", "SET ROLE regop_owner_a"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ alias, input string }{{"regoper", "regop_private.+"}, {"regoperator", "regop_private.+(integer,integer)"}, {"regoperator", "pg_catalog.+(regop_private.secret,integer)"}} {
			t.Run("allowed/"+tc.input, func(t *testing.T) {
				regoperatorDiagnosticRoutes(t, p, b, c, tc.alias, tc.input, "42883", "operator does not exist: "+tc.input, "")
			})
		}
		q := "SELECT $1::regoperator AS v,$1::regoperator::oid AS o"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER SCHEMA regop_private OWNER TO regop_owner_b", 0))
		for _, tc := range []struct{ alias, input string }{{"regoper", "regop_private.+"}, {"regoperator", "regop_private.+(integer,integer)"}, {"regoperator", "pg_catalog.+(regop_private.secret,integer)"}} {
			t.Run("denied/"+tc.input, func(t *testing.T) {
				regoperatorDiagnosticRoutes(t, p, b, c, tc.alias, tc.input, "42501", "permission denied for schema regop_private", "")
			})
		}
		regoperatorDiagnosticRoutes(t, p, b, c, "regoperator", "pg_catalog.+(secret,integer)", "42704", `type "secret" does not exist`, "")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows(q, "regoperator", "+(integer,integer)", 551, b.PG, "551"))
		var db string
		if e := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); e != nil {
			t.Fatal(e)
		}
		regoperatorDiagnosticRoutes(t, p, b, c, "regoper", fixture.RegtypeIdentifier(db)+".regop_private.+", "42501", "permission denied for schema regop_private", "")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER SCHEMA regop_private OWNER TO regop_owner_a", 0))
		s := regoperatorError(q, "42883", "operator does not exist: pg_catalog.+(regop_private.secret,integer)", "", "", "unnamed portal parameter $1 = '...'", "pg_catalog.+(regop_private.secret,integer)")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regoperatorRows(q, "regoperator", "+(integer,integer)", 551, b.PG, "+(integer,integer)"))
	})
}
