package reference_test

import (
	"bytes"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSchemaAuthorizationAndIndependentOwners(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"scauth_owner", "scauth_reader"})
	fixture.SchemaReferenceCleanup(t, "scauth_owned", "scauth_usable", "scauth_create", "scauth_denied", "scauth_renamed")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			var database, bootstrap string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database(),current_user").Scan(&database, &bootstrap); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"CREATE USER scauth_owner", "CREATE USER scauth_reader", "CREATE SCHEMA scauth_owned AUTHORIZATION scauth_owner CREATE TABLE t(id INT)", "CREATE SCHEMA scauth_usable", "CREATE SCHEMA scauth_create", "SET SESSION AUTHORIZATION scauth_owner", "CREATE TABLE scauth_owned.u(id INT)"} {
				exec(q)
			}
			run(fixture.Exec("INSERT INTO scauth_owned.t VALUES(7)", 1))
			for _, q := range []string{"CREATE SCHEMA scauth_denied", "ALTER SCHEMA scauth_owned RENAME TO scauth_renamed"} {
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for database " + database}))
			}
			exec("ALTER SCHEMA scauth_owned OWNER TO scauth_owner")
			exec("RESET SESSION AUTHORIZATION")
			for _, q := range []string{"GRANT USAGE ON SCHEMA scauth_usable TO scauth_reader", "GRANT CREATE ON SCHEMA scauth_create TO scauth_reader", "REVOKE ALL ON SCHEMA scauth_usable FROM CURRENT_USER", "REVOKE ALL ON SCHEMA scauth_create FROM CURRENT_USER", "SET SESSION AUTHORIZATION scauth_reader", "CREATE TABLE scauth_create.t(id INT)", "SET search_path TO scauth_owned,scauth_usable,public"} {
				exec(q)
			}
			run(fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{"scauth_usable"}}))
			for _, prepared := range []bool{false, true} {
				run(fixture.SchemaReferenceCreationError("CREATE TABLE no_fallback(id INT)", "42501", "permission denied for schema scauth_usable", prepared, "14"))
				run(fixture.SchemaReferenceCreationError("/* é */ CREATE TABLE scauth_usable.denied(id INT)", "42501", "permission denied for schema scauth_usable", prepared, "22"))
			}
			exec("SET search_path TO ''")
			for _, prepared := range []bool{false, true} {
				run(fixture.SchemaReferenceCreationError("CREATE TABLE no_destination(id INT)", "3F000", "no schema has been selected to create in", prepared, "14"))
				run(fixture.SchemaReferenceCreationError("/* é */ CREATE TABLE no_destination(id INT)", "3F000", "no schema has been selected to create in", prepared, "22"))
			}
			for _, q := range []string{"SELECT missing FROM scauth_owned.t", "INSERT INTO scauth_create.t VALUES('invalid')"} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("permission-denied preparation returned a statement", q)
				}
				fixture.State(t, err, "42501")
			}
			exec("RESET SESSION AUTHORIZATION")
			exec("ALTER SCHEMA scauth_owned OWNER TO scauth_reader")
			check := func() {
				run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='scauth_owned.t'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"scauth_owner"}}))
				run(fixture.Select("SELECT nspname,nspacl::text AS acl FROM pg_namespace WHERE nspname IN ('scauth_usable','scauth_create') ORDER BY nspname", []string{"nspname", "acl"}, []string{"NAME", "TEXT"}, [][]any{{"scauth_create", fmt.Sprintf("{scauth_reader=C/%s}", bootstrap)}, {"scauth_usable", fmt.Sprintf("{scauth_reader=U/%s}", bootstrap)}}))
				run(fixture.Select("SELECT id FROM scauth_owned.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.Select("SELECT id FROM scauth_owned.u", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT tablename FROM pg_tables WHERE schemaname='scauth_usable'", []string{"tablename"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT id FROM scauth_create.t", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname IN ('scauth_denied','scauth_renamed')", []string{"nspname"}, []string{"NAME"}, nil))
			}
			run(fixture.ImplicitError("ALTER SCHEMA scauth_owned OWNER TO scauth_absent", fixture.Diagnostic{Code: "42704", Message: "role \"scauth_absent\" does not exist"}))
			check()
			if !b.PG {
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil || out.Len() == 0 {
					t.Fatal("schema ACL dump failed", err, out.String())
				}
				fixture.ViewRestore(t, p, out.Bytes())
				check()
			}
			exec("SET SESSION AUTHORIZATION scauth_reader")
			exec("DROP SCHEMA scauth_owned CASCADE")
		})
	}
}
