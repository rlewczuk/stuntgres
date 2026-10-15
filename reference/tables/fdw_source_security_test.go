package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWSourceSecurity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA public OWNER TO fdw_reader", 0))
		}
		options := "user 'postgres'"
		if b.PG {
			options = "user 'stuntgres',password 'stuntgres',password_required 'false'"
		}
		q := "SELECT id,label,flag FROM fdw_data ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		mappingQuery := "SELECT usename,umoptions IS NULL AS redacted FROM pg_user_mappings WHERE srvname='fdw_live' AND usename='fdw_reader'"
		mappingStmt, err := c.PrepareContext(p.Ctx, mappingQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer mappingStmt.Close()
		mappingHeld := fixture.HeldStatement{Executor: c, Statement: mappingStmt, SQL: mappingQuery}
		mappingVisible := func(redacted bool) {
			t.Helper()
			before := r.snapshot()
			fixture.RunStep(t, p.Ctx, mappingHeld, b.Recorder, fixture.Select(mappingQuery, []string{"usename", "redacted"}, []string{"NAME", "BOOL"}, [][]any{{"fdw_reader", redacted}}))
			if !b.PG && before != r.snapshot() {
				t.Fatal("mapping catalog inspection entered foreign provider")
			}
		}
		denied := fdwDiagnostic("42501", "permission denied for foreign table fdw_data")
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		before := r.snapshot()
		run(fixture.ErrorDiagnostic(q, denied))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, denied))
		if !b.PG && r.snapshot() != before {
			t.Fatal("table ACL denial entered provider", before, r.snapshot())
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON fdw_data TO fdw_reader", 0))
		run(fixture.Exec("CREATE ROLE fdw_other", 0))
		run(fixture.Exec("GRANT fdw_other TO fdw_reader", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_other SERVER fdw_live OPTIONS("+options+")", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		before = r.snapshot()
		missing := fdwDiagnostic("42704", `user mapping not found for user "fdw_reader", server "fdw_live"`)
		run(fixture.ErrorDiagnostic(q, missing))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, missing))
		if !b.PG && r.snapshot() != before {
			t.Fatal("missing mapping entered provider", before, r.snapshot())
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+options+")", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		want := fdwRows(q, [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}})
		mappingVisible(false)
		before = r.snapshot()
		run(want)
		if !b.PG {
			fdwReleased(t, r, before, 1)
		}
		before = r.snapshot()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		if !b.PG {
			fdwReleased(t, r, before, 1)
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE SELECT ON fdw_data FROM fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		before = r.snapshot()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, denied))
		if !b.PG && r.snapshot() != before {
			t.Fatal("revoked held execution entered provider", before, r.snapshot())
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_live FROM fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		mappingVisible(true)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		mappingVisible(false)
		run(fixture.Exec("RESET ROLE", 0))
	})
}
