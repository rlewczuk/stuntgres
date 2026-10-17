package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedViewAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mv_owner", "mv_reader"})
	fixture.SchemaReferenceCleanup(t, "mv_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE USER mv_owner", 0))
			run(fixture.Exec("CREATE USER mv_reader", 0))
			run(fixture.Exec("CREATE SCHEMA mv_private AUTHORIZATION mv_owner", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION mv_owner", 0))
			fixture.WithPublicSchemaOwner(t, p, b, "mv_owner", func() {
				materializedViewSeed(run)
				run(fixture.Exec("CREATE MATERIALIZED VIEW mv_owned AS SELECT i,s FROM mv_base", 3))
			})
			run(fixture.Exec("SET SESSION AUTHORIZATION mv_reader", 0))
			s := fixture.ImplicitError("SELECT * FROM mv_owned", fixture.Diagnostic{Code: "42501", Message: "permission denied for materialized view mv_owned"})
			s.SelectRows = true
			run(s)
			run(fixture.ImplicitError("DROP MATERIALIZED VIEW mv_owned", fixture.Diagnostic{Code: "42501", Message: "must be owner of materialized view mv_owned"}))
			fixture.WithPublicSchemaOwner(t, p, b, "mv_reader", func() {
				run(fixture.ImplicitError("CREATE MATERIALIZED VIEW mv_denied AS SELECT i,s FROM mv_base", fixture.Diagnostic{Code: "42501", Message: "permission denied for table mv_base"}))
			})
			fixture.QueryTableCreateAbsent(t, run, "mv_denied")
			fixture.WithPublicSchemaOwner(t, p, b, "mv_reader", func() {
				run(fixture.Exec("CREATE MATERIALIZED VIEW mv_reader_no AS SELECT i,s FROM mv_base WITH NO DATA", 0))
			})
			run(fixture.MaterializedViewUnpopulated("mv_reader_no"))
			run(fixture.ImplicitError("CREATE MATERIALIZED VIEW mv_private.mv_denied AS SELECT 1 AS i", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema mv_private"}))
			for _, q := range []string{"INSERT INTO mv_owned VALUES(9,'z')", "UPDATE mv_owned SET i=9", "DELETE FROM mv_owned"} {
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for materialized view mv_owned"}))
			}
			fixture.WithPublicSchemaOwner(t, p, b, "mv_reader", func() { run(fixture.Exec("CREATE MATERIALIZED VIEW mv_reader_owned AS SELECT 9 AS i", 1)) })
			run(fixture.ImplicitError("DROP MATERIALIZED VIEW mv_reader_owned,mv_owned", fixture.Diagnostic{Code: "42501", Message: "must be owner of materialized view mv_owned"}))
			run(fixture.Select("SELECT i FROM mv_reader_owned", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT ON mv_owned TO mv_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION mv_reader", 0))
			run(materializedViewBag("mv_owned"))
			run(fixture.Prepared(materializedViewBag("mv_owned")))
			s = fixture.ImplicitError("SELECT * FROM mv_base", fixture.Diagnostic{Code: "42501", Message: "permission denied for table mv_base"})
			s.SelectRows = true
			run(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION mv_owner", 0))
			for _, q := range []string{"INSERT INTO mv_owned VALUES(9,'z')", "UPDATE mv_owned SET i=9", "DELETE FROM mv_owned"} {
				t.Run("read_only/"+q, func(t *testing.T) {
					tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					command := strings.Fields(q)[0]
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + command + " in a read-only transaction"}))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
			run(materializedViewBag("mv_owned"))
			run(materializedViewBag("mv_base"))
			// Schema-owner authority is distinct from MV ownership.
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_private.mv_other_owner AS SELECT 1 AS i", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION mv_owner", 0))
			run(fixture.Exec("DROP MATERIALIZED VIEW mv_private.mv_other_owner", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}

func TestReferenceMaterializedViewRestrictedSearchPath(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("SET search_path=public", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW public.mv_path AS SELECT CURRENT_SCHEMA AS ns,i FROM mv_base", 3))
			s := fixture.Select("SELECT ns,i FROM mv_path", []string{"ns", "i"}, []string{"NAME", "INT4"}, [][]any{{"pg_catalog", int64(1)}, {"pg_catalog", int64(1)}, {"pg_catalog", int64(2)}})
			s.Bag = true
			run(s)
			run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public"}}))
			run(fixture.Select("SELECT CURRENT_SCHEMA AS ns", []string{"ns"}, []string{"NAME"}, [][]any{{"public"}}))
		})
	}
}
