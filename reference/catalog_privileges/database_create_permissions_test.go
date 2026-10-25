package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseCreatePermissions(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		for _, query := range []string{
			"CREATE ROLE dc_actor CREATEDB", "CREATE ROLE dc_owner NOLOGIN NOCREATEDB",
			"CREATE ROLE dc_denied", "CREATE ROLE dc_inherit INHERIT",
			"GRANT dc_owner TO dc_actor WITH SET TRUE", "GRANT dc_actor TO dc_inherit WITH INHERIT TRUE, SET TRUE",
		} {
			c.run(fixture.Exec(query, 0))
		}
		c.run(fixture.Exec("SET ROLE dc_actor", 0))
		c.create("dc_actor_default", "", false)
		c.create("dc_owned", "OWNER dc_owner", true)
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Select("SELECT d.datname,r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname IN ('dc_actor_default','dc_owned') ORDER BY d.datname", []string{"datname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"dc_actor_default", "dc_actor"}, {"dc_owned", "dc_owner"}}))
		c.run(fixture.Exec("SET ROLE dc_inherit", 0))
		c.error("CREATE DATABASE dc_inherited", "42501", "permission denied to create database")
		c.absent("dc_inherited")
		c.run(fixture.Exec("SET ROLE dc_actor", 0))
		c.create("dc_set_role", "", false)
		c.run(fixture.Exec("RESET ROLE", 0))
		c.create("dc_acl_source", "", false)
		c.run(fixture.Exec("GRANT CREATE ON DATABASE dc_acl_source TO dc_denied", 0))
		c.run(fixture.Exec("SET ROLE dc_denied", 0))
		c.error("CREATE DATABASE dc_database_acl", "42501", "permission denied to create database")
		c.absent("dc_database_acl")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("REVOKE CREATE ON DATABASE dc_acl_source FROM dc_denied", 0))
		db, _, rec := c.pool("dc_owned", "")
		run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, db, rec, s) }
		conn, err := db.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		runOn := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, conn, rec, s) }
		runOn(fixture.Exec("SET ROLE dc_owner", 0))
		runOn(fixture.Exec("CREATE TABLE dc_owner_table(v integer)", 0))
		runOn(fixture.Exec("INSERT INTO dc_owner_table VALUES (42)", 1))
		runOn(fixture.Exec("RESET ROLE", 0))
		runOn(fixture.Exec("SET ROLE dc_denied", 0))
		d := databaseCreateDiagnostic("42501", "permission denied for schema public")
		d.Position = "14"
		runOn(fixture.ErrorDiagnostic("CREATE TABLE public.dc_denied_table(v integer)", d))
		runOn(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT v FROM dc_owner_table", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
}

func TestReferenceDatabaseCreateTemplates(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		c.run(fixture.Exec("CREATE TABLE dc_application_sentinel(v integer)", 0))
		c.run(fixture.Exec("INSERT INTO dc_application_sentinel VALUES (7)", 1))
		c.create("dc_template_a", "TEMPLATE template1", false)
		c.create("dc_template_b", "", true)
		// Copied system owners are the independently configured bootstrap roles,
		// not the new database owner or a value learned from the queried rows.
		systemOwner := "postgres"
		if c.backend.PG {
			systemOwner = "stuntgres"
		}
		for _, name := range []string{"dc_template_a", "dc_template_b"} {
			db, _, rec := c.pool(name, "")
			run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, db, rec, s) }
			run(fixture.Select("SELECT n.nspname,r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname IN ('public','pg_catalog','information_schema') ORDER BY n.nspname", []string{"nspname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"information_schema", systemOwner}, {"pg_catalog", systemOwner}, {"public", "pg_database_owner"}}))
			run(fixture.Select("SELECT c.relname,c.relkind,t.typname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_type t ON t.oid=c.reltype WHERE n.nspname='pg_catalog' AND c.relname='pg_database'", []string{"relname", "relkind", "typname"}, []string{"NAME", "CHAR", "NAME"}, [][]any{{"pg_database", "r", "pg_database"}}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='dc_application_sentinel'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TABLE dc_independent(v integer)", 0))
			run(fixture.Exec("INSERT INTO dc_independent VALUES (11)", 1))
		}
		a, _, ar := c.pool("dc_template_a", "")
		b, _, br := c.pool("dc_template_b", "")
		fixture.RunStep(t, c.p.Ctx, a, ar, fixture.Exec("UPDATE dc_independent SET v=22", 1))
		fixture.RunStep(t, c.p.Ctx, b, br, fixture.Select("SELECT v FROM dc_independent", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
		c.run(fixture.Select("SELECT v FROM dc_application_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
