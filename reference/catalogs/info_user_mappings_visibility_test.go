package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserMappingsVisibility(t *testing.T) {
	iumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SERVER ium_server FOREIGN DATA WRAPPER ium_fdw", 0))
		run(fixture.Exec("CREATE SERVER ium_hidden FOREIGN DATA WRAPPER ium_fdw", 0))
		for _, role := range []string{"ium_mapped", "ium_other", "PUBLIC"} {
			run(fixture.Exec("CREATE USER MAPPING FOR "+role+" SERVER ium_server", 0))
		}
		run(fixture.Exec("CREATE USER MAPPING FOR ium_mapped SERVER ium_hidden", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER ium_fdw TO ium_owner", 0))
		run(fixture.Exec("ALTER SERVER ium_server OWNER TO ium_owner", 0))
		run(fixture.Exec("GRANT ium_owner TO ium_member WITH INHERIT TRUE, SET TRUE", 0))
		run(fixture.Exec("GRANT ium_owner TO ium_set WITH INHERIT FALSE, SET TRUE", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ium_server TO ium_reader", 0))
		run(fixture.Exec("GRANT ium_reader TO ium_usage_member WITH INHERIT TRUE, SET TRUE", 0))
		where := "foreign_server_name IN ('ium_server','ium_hidden')"
		visible := iumRows(database, "ium_server", "ium_mapped", "ium_other", "PUBLIC")
		all := append(iumRows(database, "ium_server", "ium_mapped", "ium_other", "PUBLIC"), iumRows(database, "ium_hidden", "ium_mapped")...)
		run(iumProjection(b.PG, where, all))
		actor := func(name string, want [][]any) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+name, 0))
			s := iumProjection(b.PG, where, want)
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"ium_owner", "ium_member", "ium_reader", "ium_usage_member"} {
			actor(role, visible)
		}
		for _, role := range []string{"ium_other", "ium_mapped", "ium_set"} {
			actor(role, nil)
		}
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ium_server TO PUBLIC", 0))
		actor("ium_other", visible)
		run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER ium_server FROM PUBLIC", 0))
		actor("ium_other", nil)
		run(fixture.Exec("REVOKE USAGE ON FOREIGN DATA WRAPPER ium_fdw FROM ium_owner", 0))
		actor("ium_reader", visible) // Wrapper access is not a second read gate.

		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION ium_other", 0))
		s := iumProjection(b.PG, where, nil)
		held := iumHeld(t, p, c, r, s.Query)
		held(s)
		adminRun(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ium_server TO ium_other", 0))
		held(iumProjection(b.PG, where, visible))
		adminRun(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER ium_server FROM ium_other", 0))
		held(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))

		run(fixture.Exec("SET SESSION AUTHORIZATION ium_set", 0))
		held(s)
		run(fixture.Exec("SET ROLE ium_owner", 0))
		held(iumProjection(b.PG, where, visible))
		run(fixture.Exec("RESET ROLE", 0))
		held(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
