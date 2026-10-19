package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectRevokeFamilies(t *testing.T) {
	objectGrantScenario(t, []string{"or_schema", "or_types"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA or_schema", 0))
		run(fixture.Exec("CREATE TABLE or_schema.t(x INT)", 0))
		run(fixture.Exec("INSERT INTO or_schema.t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON or_schema.t TO og_object_reader", 0))
		run(fixture.Exec("GRANT og_object_other TO og_object_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA or_schema TO og_object_reader,og_object_other", 0))
		query := "SELECT x FROM or_schema.t"
		var held *sql.Stmt
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			var err error
			held, err = c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
		})
		defer held.Close()
		read := func() { run(fixture.Select(query, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})) }
		heldRead := func(allowed bool) {
			s := fixture.Select(query, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})
			if !allowed {
				s = fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema or_schema", Position: "15"})
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, s)
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() { read(); heldRead(true) })
		run(fixture.Exec("REVOKE USAGE ON SCHEMA or_schema FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { read(); heldRead(true) })
		run(fixture.Exec("REVOKE USAGE ON SCHEMA or_schema FROM og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, query, "schema", "or_schema", prepared)
			// PostgreSQL 18.6 also denies this same-held reader after ACL removal.
			heldRead(false)
		})
		run(fixture.Exec("GRANT USAGE ON SCHEMA or_schema TO PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { read(); heldRead(true) })
		run(fixture.Exec("REVOKE USAGE ON SCHEMA or_schema FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, query, "schema", "or_schema", prepared) })
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA or_schema TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE or_schema.created(x INT)", 0)) })
		run(fixture.Exec("REVOKE CREATE ON SCHEMA or_schema FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE or_schema.denied(x INT)", "schema", "or_schema")
			read()
		})
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='denied' AND relnamespace='or_schema'::regnamespace", []string{"relname"}, []string{"NAME"}, [][]any{}))

		run(fixture.Exec("CREATE SCHEMA or_types", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA or_types TO og_object_reader", 0))
		run(fixture.Exec("CREATE TYPE or_types.e AS ENUM('a')", 0))
		run(fixture.Exec("CREATE DOMAIN or_types.d AS INT", 0))
		for _, tc := range []struct{ kind, name, value, typ string }{{"TYPE", "e", "'a'", "TEXT"}, {"DOMAIN", "d", "7", "INT4"}} {
			target := tc.kind + " or_types." + tc.name
			run(fixture.Exec("REVOKE USAGE ON "+target+" FROM PUBLIC", 0))
			run(fixture.Exec("GRANT USAGE ON "+target+" TO og_object_reader,og_object_other", 0))
			probe := func(suffix string) {
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE or_types."+tc.name+suffix+"(x or_types."+tc.name+")", 0)) })
			}
			probe("_old")
			run(fixture.Exec("REVOKE USAGE ON "+target+" FROM og_object_reader", 0))
			probe("_inherited")
			run(fixture.Exec("REVOKE USAGE ON "+target+" FROM og_object_other", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE or_types."+tc.name+"_denied(x or_types."+tc.name+")", "type", "or_types."+tc.name)
				run(fixture.Exec("INSERT INTO or_types."+tc.name+"_old VALUES("+tc.value+")", 1))
				if tc.name == "e" {
					run(fixture.Select("SELECT x::TEXT AS v FROM or_types.e_old", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
					run(fixture.Select("SELECT 'a'::or_types.e::TEXT AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
				} else {
					run(fixture.Select("SELECT x FROM or_types.d_old", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
			run(fixture.Exec("GRANT USAGE ON "+target+" TO PUBLIC", 0))
			probe("_public")
			run(fixture.Exec("REVOKE USAGE ON "+target+" FROM PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE or_types."+tc.name+"_last(x or_types."+tc.name+")", "type", "or_types."+tc.name)
			})
		}
	})
}
