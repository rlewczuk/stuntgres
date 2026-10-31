package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ph_owner", "ph_reader"})
	pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE ph_owner", "CREATE ROLE ph_reader", "GRANT USAGE ON SCHEMA ph_part TO ph_owner,ph_reader", "ALTER TABLE ph_part.ph_heap OWNER TO ph_owner"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT * FROM ph_part.pgstathashindex($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		denied := func(arg any) {
			s := pgstatindexError(q, "42501", "permission denied for function pgstathashindex", arg)
			pgstathashindexBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		authorized := func() {
			pgstathashindexStats(t, p, b, c, q, 0, 0, "ph_part.ph_index")
			s := pgstathashindexNull(q, nil)
			pgstathashindexBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION ph_owner", 0))
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part.ph_heap", []string{"k"}, []string{"INT4"}, nil))
		denied(nil)
		denied("ph_part.ph_index")
		pgstathashindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ph_part.pgstathashindex(NULL)", "42501", "permission denied for function pgstathashindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ph_part.pgstathashindex(regclass) TO ph_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ph_reader", 0))
		pgstathashindexBoth(t, p, b, c, pgstatindexError("SELECT k FROM ph_part.ph_heap", "42501", "permission denied for table ph_heap"))
		authorized()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ph_part.pgstathashindex(regclass) FROM ph_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ph_reader", 0))
		denied(nil)
		denied("ph_part.ph_index")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"pg_stat_scan_tables", "pg_monitor"} {
			run(fixture.Exec("GRANT "+role+" TO ph_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION ph_reader", 0))
			authorized()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE "+role+" FROM ph_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION ph_reader", 0))
			denied(nil)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
	t.Run("schema_usage", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ph_no_schema"})
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE ph_no_schema", "GRANT EXECUTE ON FUNCTION ph_part.pgstathashindex(regclass) TO ph_no_schema", "SET SESSION AUTHORIZATION ph_no_schema"} {
				run(fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			q := "SELECT * FROM ph_part.pgstathashindex(NULL::regclass)"
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ph_part")
			d.Position = "15"
			s := fixture.ImplicitError(q, d)
			s.SelectRows = true
			run(s)
			st, err := c.PrepareContext(p.Ctx, q)
			if st != nil {
				st.Close()
				t.Fatal("schema-denied call prepared")
			}
			fixture.AssertDiagnostic(t, err, &d)
		})
	})
	t.Run("argument_order", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ph_argument_reader"})
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE ph_argument_reader", "GRANT USAGE ON SCHEMA ph_part TO ph_argument_reader"} {
				run(fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			// Bound constant parameters can fail during custom-plan folding,
			// before function ACL initialization. pg_backend_pid keeps this
			// division in the executor, as in the existing B-tree witness.
			q := "SELECT * FROM ph_part.pgstathashindex((1/(pg_backend_pid()*0))::regclass)"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, allowed := range []bool{false, true, false} {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				grant := "REVOKE EXECUTE ON FUNCTION ph_part.pgstathashindex(regclass) FROM ph_argument_reader"
				if allowed {
					grant = "GRANT EXECUTE ON FUNCTION ph_part.pgstathashindex(regclass) TO ph_argument_reader"
				}
				run(fixture.Exec(grant, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ph_argument_reader", 0))
				code, message := "42501", "permission denied for function pgstathashindex"
				if allowed {
					code, message = "22012", "division by zero"
				}
				s := pgstatindexError(q, code, message)
				pgstathashindexBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}
