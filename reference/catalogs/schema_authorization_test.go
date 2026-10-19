package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sa_owner", "sa_other"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "sa_owned", "sa_other_schema")
	for _, q := range []string{"CREATE USER sa_owner", "CREATE USER sa_other", "CREATE SCHEMA sa_owned AUTHORIZATION sa_owner CREATE TABLE t(id INT)", "CREATE SCHEMA sa_other_schema AUTHORIZATION sa_other CREATE TABLE t(id INT)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO sa_other_schema.t VALUES(9)", 1))
	// Ordinary-role database CREATE is absent on both fixtures. Native has no ACL.
	if p.Postgres != nil {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "REVOKE CREATE ON DATABASE stuntgres_reference FROM PUBLIC,sa_owner,sa_other"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			// PostgreSQL reports the raw current database name; native's comes
			// from the fixture's per-test connector name.
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION sa_owner", 0))
			run(fixture.Prepared(fixture.Exec("CREATE TABLE sa_owned.u(id INT)", 0)))
			run(fixture.Exec("INSERT INTO sa_owned.t VALUES(7)", 1))
			run(fixture.Prepared(fixture.Select("SELECT id FROM sa_owned.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})))
			s := fixture.RoleReferenceError("CREATE SCHEMA sa_denied", "42501", "permission denied for database "+database, "", true, "execute")
			run(s)
			run(fixture.RoleReferenceError("ALTER SCHEMA sa_owned RENAME TO sa_new", "42501", "permission denied for database "+database, "", true, "execute"))
			denied := fixture.RoleReferenceError("SELECT id FROM sa_other_schema.t", "42501", "permission denied for schema sa_other_schema", "", true, "prepare")
			denied.Diagnostic.Position = "16"
			run(denied)
			run(fixture.Exec("SET search_path TO sa_other_schema,sa_owned", 0))
			run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"sa_owned"}}))
			run(fixture.Prepared(fixture.Exec("ALTER SCHEMA sa_owned OWNER TO sa_owner", 0)))
			for _, prepared := range []bool{false, true} {
				run(fixture.RoleReferenceError("DROP SCHEMA sa_owned,sa_other_schema CASCADE", "42501", "must be owner of schema sa_other_schema", "", prepared, "execute"))
				run(fixture.Prepared(fixture.Select("SELECT id FROM sa_owned.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})))
			}
			run(fixture.Exec("DROP SCHEMA sa_owned CASCADE", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT id FROM sa_other_schema.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}
