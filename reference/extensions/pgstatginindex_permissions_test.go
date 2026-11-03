package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gg_owner", "gg_reader"})
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgstatginindexBuild(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE gg_owner", "CREATE ROLE gg_reader", "GRANT USAGE ON SCHEMA gg_part TO gg_owner,gg_reader", "ALTER TABLE gg_part.gg_heap OWNER TO gg_owner"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT * FROM gg_part.pgstatginindex($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		denied := func(arg any) {
			s := pgstatindexError(q, "42501", "permission denied for function pgstatginindex", arg)
			pgstatginindexBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		authorized := func() {
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats(q, "gg_part.gg_array"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstatginindexStats(q, "gg_part.gg_array"))
			s := pgstatginindexNull(q, nil)
			pgstatginindexBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION gg_owner", 0))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT id FROM gg_part.gg_heap", []string{"id"}, []string{"INT4"}, nil))
		denied(nil)
		denied("gg_part.gg_array")
		pgstatginindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM gg_part.pgstatginindex(NULL)", "42501", "permission denied for function pgstatginindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON gg_part.gg_heap TO gg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION gg_reader", 0))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT id FROM gg_part.gg_heap", []string{"id"}, []string{"INT4"}, nil))
		denied(nil)
		denied("gg_part.gg_array")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON gg_part.gg_heap FROM gg_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION gg_part.pgstatginindex(regclass) TO gg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION gg_reader", 0))
		pgstatginindexBoth(t, p, b, c, pgstatindexError("SELECT id FROM gg_part.gg_heap", "42501", "permission denied for table gg_heap"))
		authorized()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION gg_part.pgstatginindex(regclass) FROM gg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION gg_reader", 0))
		denied(nil)
		denied("gg_part.gg_array")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"pg_stat_scan_tables", "pg_monitor"} {
			run(fixture.Exec("GRANT "+role+" TO gg_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION gg_reader", 0))
			authorized()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE "+role+" FROM gg_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION gg_reader", 0))
			denied(nil)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
	t.Run("schema_usage", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"gg_no_schema"})
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pgstatginindexBuild(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE gg_no_schema", "GRANT EXECUTE ON FUNCTION gg_part.pgstatginindex(regclass) TO gg_no_schema", "SET SESSION AUTHORIZATION gg_no_schema"} {
				run(fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			q := "SELECT * FROM gg_part.pgstatginindex(NULL::regclass)"
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema gg_part")
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
		fixture.RoleReferenceCleanup(t, []string{"gg_argument_reader"})
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pgstatginindexBuild(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE gg_argument_reader", "GRANT USAGE ON SCHEMA gg_part TO gg_argument_reader"} {
				run(fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			// Bound constant parameters can fail during custom-plan folding,
			// before function ACL initialization. pg_backend_pid keeps this
			// division in the executor, as in the existing B-tree witness.
			q := "SELECT * FROM gg_part.pgstatginindex((1/(pg_backend_pid()*0))::regclass)"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, allowed := range []bool{false, true, false} {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				grant := "REVOKE EXECUTE ON FUNCTION gg_part.pgstatginindex(regclass) FROM gg_argument_reader"
				if allowed {
					grant = "GRANT EXECUTE ON FUNCTION gg_part.pgstatginindex(regclass) TO gg_argument_reader"
				}
				run(fixture.Exec(grant, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION gg_argument_reader", 0))
				code, message := "42501", "permission denied for function pgstatginindex"
				if allowed {
					code, message = "22012", "division by zero"
				}
				s := pgstatindexError(q, code, message)
				pgstatginindexBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}
