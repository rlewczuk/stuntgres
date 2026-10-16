package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Enum-array storage is not a native provider. Preserve PostgreSQL's actual
// element-ACL outcome and assert the positioned native admission boundary.
func dpEnumArray(run func(fixture.Step), b fixture.Backend, query, element string, allowed bool) {
	if b.PG {
		if allowed {
			run(fixture.Exec(query, 0))
		} else {
			fixture.RoleGrantPermissionDenied(run, query, "type", element)
		}
		return
	}
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "unsupported array element type", Position: fixture.Position(strings.LastIndex(query, element) + 1)})
	s.ErrorStage = "execute"
	run(s)
}

func TestReferenceRoleDefaultPrivilegesTypes(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_s", "dp_reader")
		run(fixture.Exec("CREATE TYPE dp_s.dp_enum_old AS ENUM('a')", 0))
		run(fixture.Exec("CREATE DOMAIN dp_s.dp_domain_old AS INT", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_composite_old AS(x INT)", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_enum_new AS ENUM('a')", 0))
		run(fixture.Exec("CREATE DOMAIN dp_s.dp_domain_new AS INT", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_composite_new AS(x INT)", 0))
		// A future table grant for the creator cannot bypass source TYPES USAGE.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_reader GRANT SELECT ON TABLES TO dp_other", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.WithPublicSchemaOwner(t, p, b, "dp_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE dp_typed_source_denied OF dp_s.dp_composite_new", "type", "dp_s.dp_composite_new")
			})
			for _, name := range []string{"enum", "domain", "composite"} {
				run(fixture.Exec("CREATE TABLE dp_s.dp_"+name+"_old_use(x dp_s.dp_"+name+"_old)", 0))
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE dp_s.dp_"+name+"_new_use(x dp_s.dp_"+name+"_new)", "type", "dp_s.dp_"+name+"_new")
			}
			dpEnumArray(run, b, "CREATE TABLE dp_s.dp_array_old_use(x dp_s.dp_enum_old[])", "dp_s.dp_enum_old", true)
			dpEnumArray(run, b, "CREATE TABLE dp_s.dp_array_new_use(x dp_s.dp_enum_new[])", "dp_s.dp_enum_new", false)
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT USAGE ON TYPES TO dp_reader", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_enum_scoped AS ENUM('b')", 0))
		run(fixture.Exec("CREATE DOMAIN dp_s.dp_domain_scoped AS INT", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_composite_scoped AS(x INT)", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			for _, name := range []string{"enum", "domain", "composite"} {
				run(fixture.Exec("CREATE TABLE dp_s.dp_"+name+"_scoped_use(x dp_s.dp_"+name+"_scoped)", 0))
			}
			dpEnumArray(run, b, "CREATE TABLE dp_s.dp_array_scoped_use(x dp_s.dp_enum_scoped[])", "dp_s.dp_enum_scoped", true)
			fixture.WithPublicSchemaOwner(t, p, b, "dp_reader", func() { run(fixture.Exec("CREATE TABLE dp_typed_scoped_source OF dp_s.dp_composite_scoped", 0)) })
			run(fixture.Exec("INSERT INTO dp_typed_scoped_source VALUES(13)", 1))
			dpRead(run, "dp_typed_scoped_source", 13)
			fixture.WithPublicSchemaOwner(t, p, b, "dp_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE dp_typed_source_still_denied OF dp_s.dp_composite_new", "type", "dp_s.dp_composite_new")
			})
			run(fixture.Exec("INSERT INTO dp_s.dp_enum_scoped_use VALUES('b')", 1))
			run(fixture.Select("SELECT x::TEXT FROM dp_s.dp_enum_scoped_use", []string{"x"}, []string{"TEXT"}, [][]any{{"b"}}))
			run(fixture.Exec("INSERT INTO dp_s.dp_domain_scoped_use VALUES(9)", 1))
			run(fixture.Select("SELECT x::INT FROM dp_s.dp_domain_scoped_use", []string{"x"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
		fixture.RoleGrantActor(run, "dp_other", func() { dpRead(run, "dp_typed_scoped_source", 13) })
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_typed_scoped_source' AND grantee='dp_other'", [][]any{{"dp_reader", "dp_other", "public", "dp_typed_scoped_source", "SELECT", "NO", "YES"}})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE USAGE ON TYPES FROM dp_reader", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_enum_next AS ENUM('c')", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE dp_s.dp_enum_next_use(x dp_s.dp_enum_next)", "type", "dp_s.dp_enum_next")
			run(fixture.Exec("CREATE TABLE dp_s.dp_enum_still_use(x dp_s.dp_enum_scoped)", 0))
		})
		// Dependent table rowtypes do not consume standalone TYPES directives.
		run(fixture.Exec("CREATE TABLE dp_s.dp_row_source(x INT)", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() { run(fixture.Exec("CREATE TABLE dp_s.dp_row_use(x dp_s.dp_row_source)", 0)) })
		// Standalone composite backing relations never consume TABLES defaults.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_other", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT USAGE ON TYPES TO dp_other", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_composite_acl AS(x INT)", 0))
		dpProjection(t, p, c, run, "table_schema='dp_s' AND table_name='dp_composite_acl'", nil)
	})
}

func TestReferenceRoleDefaultPrivilegesSchemas(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA dp_s AUTHORIZATION dp_owner", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT USAGE ON SCHEMAS TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("CREATE SCHEMA dp_owner_ns AUTHORIZATION dp_owner CREATE TABLE dp_schema_table(x INT)", 0))
		fixture.RoleGrantActor(run, "dp_owner", func() {
			run(fixture.Exec("INSERT INTO dp_owner_ns.dp_schema_table VALUES(9)", 1))
			dpTable(run, "dp_s.dp_schema_old_table", 7)
		})
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_s.dp_schema_old_table", "schema", "dp_s", prepared)
			dpRead(run, "dp_owner_ns.dp_schema_table", 9)
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE dp_owner_ns.dp_cannot_create(x INT)", "schema", "dp_owner_ns")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT CREATE ON SCHEMAS TO dp_reader", 0))
		run(fixture.Exec("CREATE SCHEMA dp_new_ns AUTHORIZATION dp_owner", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() { dpTable(run, "dp_new_ns.dp_can_create", 11); dpRead(run, "dp_new_ns.dp_can_create", 11) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE USAGE,CREATE ON SCHEMAS FROM dp_reader", 0))
		run(fixture.Exec("CREATE SCHEMA dp_t AUTHORIZATION dp_owner", 0))
		fixture.RoleGrantActor(run, "dp_owner", func() { dpTable(run, "dp_t.dp_schema_next_table", 13) })
		fixture.RoleGrantActor(run, "dp_reader", func() {
			dpRead(run, "dp_owner_ns.dp_schema_table", 9)
			dpTable(run, "dp_new_ns.dp_still_create", 17)
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_t.dp_schema_next_table", "schema", "dp_t", prepared)
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE dp_t.dp_cannot_create(x INT)", "schema", "dp_t")
		})
		// Namespace creator is named authorization owner, not superuser issuer.
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_schema_table' AND grantee='dp_reader'", [][]any{{"dp_owner", "dp_reader", "dp_owner_ns", "dp_schema_table", "SELECT", "NO", "YES"}})
		// Automatic TEMP namespaces do not consume explicit SCHEMAS defaults.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE,CREATE ON SCHEMAS TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE,CREATE ON SCHEMAS FROM CURRENT_USER", 0))
		run(fixture.Exec("CREATE TEMP TABLE dp_temp_namespace(x INT)", 0))
		run(fixture.Exec("INSERT INTO dp_temp_namespace VALUES(7)", 1))
		dpRead(run, "dp_temp_namespace", 7)
		run(fixture.Exec("GRANT SELECT ON dp_temp_namespace TO dp_reader", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() { dpRead(run, "dp_temp_namespace", 7) })
	})
}
