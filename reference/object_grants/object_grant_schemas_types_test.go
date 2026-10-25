package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Object-grant utilities and consumers share direct/prepared routes and fixtures.
func objectGrantScenario(t *testing.T, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"og_object_reader", "og_object_other"})
			for _, schema := range schemas {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) {
						t.Helper()
						s.Prepared = prepared
						objectGrantStep(t, p.Ctx, c, b.Recorder, s)
					}
					run(fixture.Exec("CREATE ROLE og_object_reader", 0))
					run(fixture.Exec("CREATE ROLE og_object_other", 0))
					body(t, p, b, c, run, prepared)
				})
			}
		})
	}
}

// objectGrantStep runs one step on the caller's goroutine, not as a subtest,
// and stops the test at the step's first failure, logging its SQL.
func objectGrantStep(t *testing.T, ctx context.Context, executor fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	failed := t.Failed()
	defer func() {
		if !failed && t.Failed() {
			t.Logf("failed step on %T: %q args %v", executor, s.Query, s.Args)
		}
	}()
	fixture.RunStep(t, ctx, executor, recorder, s)
	if !failed && t.Failed() {
		t.FailNow()
	}
}

func TestReferenceObjectGrantSchemasTypes(t *testing.T) {
	objectGrantScenario(t, []string{"og_acl", "og_types", "og_late"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA og_acl", 0))
		run(fixture.Exec("CREATE SCHEMA og_types", 0))
		run(fixture.Exec("CREATE SCHEMA og_late", 0))
		run(fixture.Select("SELECT nspacl IS NULL AS absent FROM pg_namespace WHERE nspname='og_acl'", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("CREATE TABLE og_acl.t(x INT)", 0))
		run(fixture.Exec("INSERT INTO og_acl.t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON og_acl.t TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM og_acl.t", "schema", "og_acl", prepared)
		})
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_acl TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT x FROM og_acl.t", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_acl.no_create(x INT)", "schema", "og_acl")
		})
		run(fixture.Exec("GRANT CREATE ON SCHEMA og_acl TO og_object_reader", 0))
		run(fixture.Exec("REVOKE SELECT ON og_acl.t FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("CREATE TABLE og_acl.created(x INT)", 0))
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM og_acl.t", "table", "t")
		})
		fixture.RoleGrantError(run, "GRANT USAGE ON SCHEMA og_late,og_missing TO og_object_reader", "3F000", `schema "og_missing" does not exist`)
		run(fixture.Exec("CREATE TABLE og_late.t(x INT)", 0))
		run(fixture.Exec("GRANT SELECT ON og_late.t TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM og_late.t", "schema", "og_late", prepared)
		})

		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_types TO og_object_reader", 0))
		run(fixture.Exec("CREATE TYPE og_types.e AS ENUM('a')", 0))
		run(fixture.Exec("CREATE DOMAIN og_types.d AS INT", 0))
		run(fixture.Exec("CREATE TYPE og_types.c AS(x INT)", 0))
		for _, name := range []string{"e", "d", "c"} {
			fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE og_types.old_"+name+"(x og_types."+name+")", 0)) })
			run(fixture.Exec("REVOKE USAGE ON TYPE og_types."+name+" FROM PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_types.denied_"+name+"(x og_types."+name+")", "type", "og_types."+name)
			})
			run(fixture.Exec("GRANT ALL PRIVILEGES ON TYPE og_types."+name+" TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE og_types.new_"+name+"(x og_types."+name+")", 0)) })
		}
		run(fixture.Exec("REVOKE USAGE ON TYPE og_types.e FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("INSERT INTO og_types.old_e VALUES('a')", 1))
			run(fixture.Select("SELECT x::TEXT FROM og_types.old_e", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}}))
			run(fixture.Select("SELECT 'a'::og_types.e::TEXT AS x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}}))
		})
		run(fixture.Exec("GRANT USAGE ON DOMAIN og_types.d TO og_object_reader", 0))
		fixture.RoleGrantError(run, "GRANT USAGE ON DOMAIN og_types.e TO og_object_reader", "42809", `"og_types.e" is not a domain`)
		run(fixture.ImplicitError("GRANT USAGE ON TYPE pg_catalog._int4 TO og_object_reader", fixture.Diagnostic{Code: "0LP01", Message: "cannot set privileges of array types", Hint: "Set the privileges of the element type instead."}))
		fixture.RoleGrantError(run, "GRANT USAGE ON TYPE og_types.e,og_types.missing TO og_object_other", "42704", `type "og_types.missing" does not exist`)
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_types TO og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_types.late_denied(x og_types.e)", "type", "og_types.e")
		})

		// Restore global builtin PUBLIC rights even if any oracle assertion fails.
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE pg_catalog.int4 TO PUBLIC", "REVOKE USAGE ON TYPE pg_catalog.int4 FROM og_object_reader"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("builtin ACL cleanup", q, err)
				}
			}
		}()
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE og_types.builtin_old(x INT)", 0)) })
		run(fixture.Exec("REVOKE USAGE ON TYPE pg_catalog.int4 FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_types.builtin_denied(x INT)", "type", "integer")
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_types.builtin_array_denied(x INT[])", "type", "integer")
			run(fixture.Exec("INSERT INTO og_types.builtin_old VALUES(9)", 1))
			run(fixture.Select("SELECT x FROM og_types.builtin_old", []string{"x"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Select("SELECT '11'::INT AS x", []string{"x"}, []string{"INT4"}, [][]any{{int64(11)}}))
		})
		run(fixture.Exec("GRANT USAGE ON TYPE pg_catalog.int4 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("CREATE TABLE og_types.builtin_new(x INT)", 0))
			run(fixture.Exec("CREATE TABLE og_types.builtin_array_new(x INT[])", 0))
		})
	})
}
