package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ps_owner", "ps_reader"})
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE ps_owner", "CREATE ROLE ps_reader", "GRANT USAGE ON SCHEMA ps_part TO ps_owner,ps_reader", "ALTER TABLE ps_part.ps_heap OWNER TO ps_owner"} {
			run(fixture.Exec(q, 0))
		}
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		held := make([]fixture.HeldStatement, 2)
		for i, typ := range []string{"text", "regclass"} {
			q := "SELECT * FROM ps_part.pgstatindex($1::" + typ + ")"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		}
		denied := func(i int, arg any) {
			s := pgstatindexError(held[i].SQL, "42501", "permission denied for function pgstatindex", arg)
			pgstatindexBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
		}
		authorized := func(i int) {
			pgstatindexEmpty(t, p, c, held[i].SQL, "ps_part.ps_index")
			s := pgstatindexNull(held[i].SQL, nil)
			pgstatindexBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
		}
		// Table ownership (and hence SELECT) supplies no function privilege.
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_owner", 0))
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ps_part.ps_heap", []string{"k"}, []string{"INT4"}, nil))
		for i := range held {
			denied(i, nil)
			denied(i, "ps_part.ps_index")
		}
		// Function authorization precedes execution of a text name lookup.
		denied(0, "ps_part.ps_missing")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))

		// An unrelated reader has no SELECT; individual EXECUTE ACLs are enough.
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ps_part.pgstatindex(text) TO ps_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_reader", 0))
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT k FROM ps_part.ps_heap", "42501", "permission denied for table ps_heap"))
		authorized(0)
		denied(1, nil)
		denied(1, "ps_part.ps_index")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ps_part.pgstatindex(text) FROM ps_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ps_part.pgstatindex(regclass) TO ps_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_reader", 0))
		denied(0, nil)
		authorized(1)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ps_part.pgstatindex(regclass) FROM ps_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_reader", 0))
		for i := range held {
			denied(i, nil)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))

		// Exercise actual predefined role membership, including pg_monitor's
		// inheritance of pg_stat_scan_tables; never a name-based bypass.
		for _, role := range []string{"pg_stat_scan_tables", "pg_monitor"} {
			run(fixture.Exec("GRANT "+role+" TO ps_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION ps_reader", 0))
			for i := range held {
				authorized(i)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE "+role+" FROM ps_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION ps_reader", 0))
			for i := range held {
				denied(i, nil)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}
