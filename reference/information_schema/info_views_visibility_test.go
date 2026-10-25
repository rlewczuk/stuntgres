package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewsVisibility(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, role := range ivRoles {
			q := "CREATE ROLE " + role
			if role == "iv_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{
			"CREATE SCHEMA iv_private", "REVOKE ALL ON SCHEMA iv_private FROM PUBLIC",
			"CREATE TABLE iv_private.base(id integer)", "CREATE VIEW iv_private.v AS SELECT id FROM iv_private.base",
			"ALTER VIEW iv_private.v OWNER TO iv_owner", "REVOKE ALL ON iv_private.v FROM PUBLIC",
			"GRANT iv_owner TO iv_inherit WITH INHERIT TRUE, SET TRUE",
			"GRANT iv_owner TO iv_set WITH INHERIT FALSE, SET TRUE", "GRANT iv_owner TO iv_noinherit",
			"GRANT SELECT ON iv_private.v TO iv_reader", "GRANT REFERENCES(id) ON iv_private.v TO iv_column",
			"GRANT USAGE ON SCHEMA iv_private TO iv_schema", "GRANT MAINTAIN ON iv_private.v TO iv_maintain",
		} {
			run(fixture.Exec(q, 0))
		}
		full := ivStep(b.PG, ivSelect, [][]any{ivRow(database, "iv_private", "v", " SELECT id\n   FROM iv_private.base;", "NONE", "YES", "YES")}, "iv_private", "v")
		acl := ivStep(b.PG, ivSelect, [][]any{ivRow(database, "iv_private", "v", nil, "NONE", "YES", "YES")}, "iv_private", "v")
		empty := ivStep(b.PG, ivSelect, nil, "iv_private", "v")
		actor := func(role string, s fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(full) // Bootstrap superuser, not the defining role.
		for _, role := range []string{"iv_owner", "iv_inherit"} {
			actor(role, full)
		}
		for _, role := range []string{"iv_reader", "iv_column"} {
			actor(role, acl)
		}
		for _, role := range []string{"iv_set", "iv_noinherit", "iv_schema", "iv_maintain", "iv_unrelated"} {
			actor(role, empty)
		}
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION iv_reader", 0))
		held := ivHeld(t, p, b, c, ivSelect)
		held(acl)
		adminRun(fixture.Exec("REVOKE SELECT ON iv_private.v FROM iv_reader", 0))
		held(empty)
		adminRun(fixture.Exec("GRANT UPDATE(id) ON iv_private.v TO iv_reader", 0))
		held(acl)
		adminRun(fixture.Exec("REVOKE UPDATE(id) ON iv_private.v FROM iv_reader", 0))
		held(empty)
		adminRun(fixture.Exec("GRANT iv_owner TO iv_reader WITH INHERIT TRUE, SET TRUE", 0))
		held(full)
		adminRun(fixture.Exec("REVOKE iv_owner FROM iv_reader", 0))
		held(empty)
		adminRun(fixture.Exec("GRANT SELECT ON iv_private.v TO PUBLIC", 0))
		held(acl)
		adminRun(fixture.Exec("REVOKE SELECT ON iv_private.v FROM PUBLIC", 0))
		held(empty)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION iv_set", 0))
		held(empty)
		run(fixture.Exec("SET ROLE iv_owner", 0))
		held(full)
		run(fixture.Exec("RESET ROLE", 0))
		held(empty)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION iv_owner", 0))
		held(full)
		adminRun(fixture.Exec("ALTER VIEW iv_private.v OWNER TO iv_newowner", 0))
		held(empty)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		actor("iv_newowner", full)
	})
}

func TestReferenceInfoViewsTemporary(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		other, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		otherRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, other, b.Recorder, s) }
		run(fixture.Exec("CREATE TEMP VIEW iv_temp AS SELECT 1 AS n", 0))
		otherRun(fixture.Exec("CREATE TEMP VIEW iv_other_temp AS SELECT 2 AS n", 0))
		run(fixture.Exec("CREATE VIEW iv_temp_control AS SELECT 3 AS n", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW iv_materialized AS SELECT 4 AS n", 1))
		var schema string
		if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='iv_temp'::regclass").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		q := "SELECT * FROM information_schema.views WHERE table_name IN ('iv_temp','iv_other_temp','iv_temp_control','iv_materialized')"
		want := [][]any{ivRow(database, schema, "iv_temp", " SELECT 1 AS n;", "NONE", "NO", "NO"), ivRow(database, "public", "iv_temp_control", " SELECT 3 AS n;", "NONE", "NO", "NO")}
		s := ivStep(b.PG, q, want)
		run(s)
		run(fixture.Prepared(s))
		ivHeld(t, p, b, c, q)(s)
		var otherSchema string
		if err := other.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='iv_other_temp'::regclass").Scan(&otherSchema); err != nil {
			t.Fatal(err)
		}
		if schema == otherSchema {
			t.Fatalf("physical TEMP namespaces must differ: %q", schema)
		}
		otherRun(ivStep(b.PG, q, [][]any{ivRow(database, otherSchema, "iv_other_temp", " SELECT 2 AS n;", "NONE", "NO", "NO"), want[1]}))
	})
}
