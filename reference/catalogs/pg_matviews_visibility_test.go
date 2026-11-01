package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGMatviewsVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgmv_reader", "pgmv_owner"})
	fixture.SchemaReferenceCleanup(t, "pgmv_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
			defer owner.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			defer reader.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE pgmv_reader", "CREATE ROLE pgmv_owner", "CREATE SCHEMA pgmv_private AUTHORIZATION pgmv_owner", "SET SESSION AUTHORIZATION pgmv_owner", "CREATE TABLE pgmv_private.pgmv_base(n INT)", "CREATE MATERIALIZED VIEW pgmv_private.pgmv_visible AS SELECT n FROM pgmv_private.pgmv_base WITH NO DATA", "RESET SESSION AUTHORIZATION", "CREATE TABLE public.pgmv_base(n INT)"} {
				run(owner, fixture.Exec(q, 0))
			}
			run(owner, fixture.Exec("INSERT INTO public.pgmv_base VALUES(7)", 1))
			run(owner, fixture.Exec("CREATE MATERIALIZED VIEW public.pgmv_visible AS SELECT n FROM public.pgmv_base", 1))
			if b.PG {
				run(owner, fixture.Exec("GRANT USAGE ON SCHEMA public TO pgmv_reader", 0))
			}
			query := "SELECT * FROM pg_catalog.pg_matviews WHERE matviewname='pgmv_visible' ORDER BY schemaname"
			stmt, err := reader.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: query}
			s := fixture.PGTableViewSelect(b, query, fixture.PGMatviewColumns, fixture.PGMatviewTypes, [][]any{
				fixture.PGMatviewRow("pgmv_private", "pgmv_visible", "pgmv_owner", false, false, " SELECT n\n   FROM pgmv_private.pgmv_base;"),
				fixture.PGMatviewRow("public", "pgmv_visible", fixture.PGTableViewsOwner(b), false, true, " SELECT n\n   FROM pgmv_base;"),
			})
			check := func(e fixture.Executor) {
				run(e, s)
				run(e, fixture.Prepared(s))
			}
			run(reader, fixture.Exec("SET SESSION AUTHORIZATION pgmv_reader", 0))
			run(reader, fixture.Exec("SET search_path TO public", 0))
			check(reader)
			run(held, s)
			for _, c := range []struct{ query, message, position, stage string }{
				{"SELECT n FROM pgmv_private.pgmv_visible", "permission denied for schema pgmv_private", "15", "prepare"},
				{"SELECT n FROM public.pgmv_visible", "permission denied for materialized view pgmv_visible", "", "execute"},
			} {
				for _, prepared := range []bool{false, true} {
					errStep := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "42501", Message: c.message, Position: c.position})
					errStep.SelectRows, errStep.Prepared = true, prepared
					if prepared {
						errStep.ErrorStage = c.stage
					}
					run(reader, errStep)
				}
			}
			run(owner, fixture.Exec("CREATE TEMP TABLE pgmv_visible(n INT)", 0))
			run(owner, fixture.Exec("INSERT INTO pgmv_visible VALUES(11)", 1))
			run(owner, fixture.Select("SELECT n FROM pgmv_visible", []string{"n"}, []string{"INT4"}, [][]any{{int64(11)}}))
			check(owner)
			run(held, s)
			run(owner, fixture.Exec("DROP TABLE pg_temp.pgmv_visible", 0))
			check(owner)
			run(held, s)
			defer func() {
				if _, err := owner.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_matviews TO PUBLIC"); err != nil {
					t.Error(err)
				}
			}()
			run(owner, fixture.Exec("REVOKE SELECT ON pg_catalog.pg_matviews FROM PUBLIC", 0))
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_matviews"})
			denied.SelectRows, denied.ErrorStage = true, "execute"
			run(reader, denied)
			run(reader, fixture.Prepared(denied))
			run(held, denied)
			run(owner, fixture.Exec("GRANT SELECT ON pg_catalog.pg_matviews TO PUBLIC", 0))
			check(reader)
			run(held, s)
		})
	}
}
