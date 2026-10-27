package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserMappingsLifecycle(t *testing.T) {
	iumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		where := "foreign_server_name IN ('ium_server','IUM Renamed Server')"
		empty := iumProjection(b.PG, where, nil)
		held := iumHeld(t, p, c, r, empty.Query)
		held(empty)
		run(fixture.Exec("CREATE SERVER ium_server FOREIGN DATA WRAPPER ium_fdw", 0))
		held(empty)
		run(fixture.Exec("CREATE USER MAPPING FOR ium_mapped SERVER ium_server", 0))
		held(iumProjection(b.PG, where, iumRows(database, "ium_server", "ium_mapped")))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER ium_server", 0))
		baseline := iumRows(database, "ium_server", "ium_mapped", "PUBLIC")
		held(iumProjection(b.PG, where, baseline))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR ium_other SERVER ium_server", 0))
		held(iumProjection(b.PG, where, iumRows(database, "ium_server", "ium_mapped", "PUBLIC", "ium_other")))
		run(fixture.Exec("ROLLBACK", 0))
		held(iumProjection(b.PG, where, baseline))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_drop", 0))
		run(fixture.Exec("DROP USER MAPPING FOR ium_mapped SERVER ium_server", 0))
		held(iumProjection(b.PG, where, iumRows(database, "ium_server", "PUBLIC")))
		run(fixture.Exec("ROLLBACK TO before_drop", 0))
		held(iumProjection(b.PG, where, baseline))
		run(fixture.Exec("COMMIT", 0))
		held(iumProjection(b.PG, where, baseline))
		mappedName := "IUM Renamed Role"
		run(fixture.Exec(`ALTER ROLE ium_mapped RENAME TO "IUM Renamed Role"`, 0))
		held(iumProjection(b.PG, where, iumRows(database, "ium_server", mappedName, "PUBLIC")))
		run(fixture.Exec(`ALTER SERVER ium_server RENAME TO "IUM Renamed Server"`, 0))
		held(iumProjection(b.PG, where, iumRows(database, "IUM Renamed Server", mappedName, "PUBLIC")))
		run(iumProjection(b.PG, "foreign_server_name='ium_server'", nil))
		run(fixture.Exec(`DROP USER MAPPING FOR "`+mappedName+`" SERVER "IUM Renamed Server"`, 0))
		held(iumProjection(b.PG, where, iumRows(database, "IUM Renamed Server", "PUBLIC")))
		*notices = nil
		run(fixture.Exec(`DROP SERVER "IUM Renamed Server" CASCADE`, 0))
		iumCascadeNotice(t, b.PG, *notices, "IUM Renamed Server", "public")
		held(empty)
	})
}

func TestReferenceInfoUserMappingsSmoke(t *testing.T) {
	iumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SERVER ium_server FOREIGN DATA WRAPPER ium_fdw", 0))
		for _, role := range []string{"ium_mapped", "ium_other", "PUBLIC"} {
			run(fixture.Exec("CREATE USER MAPPING FOR "+role+" SERVER ium_server OPTIONS(password 'ium_smoke_secret')", 0))
		}
		where := "foreign_server_name='ium_server'"
		visible := iumProjection(b.PG, where, iumRows(database, "ium_server", "ium_mapped", "ium_other", "PUBLIC"))
		run(visible)
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION ium_reader", 0))
		empty := iumProjection(b.PG, where, nil)
		held := iumHeld(t, p, c, r, empty.Query)
		held(empty)
		adminRun(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ium_server TO ium_reader", 0))
		held(visible)
		adminRun(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER ium_server FROM ium_reader", 0))
		held(empty)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP USER MAPPING FOR ium_mapped SERVER ium_server", 0))
		held(iumProjection(b.PG, where, iumRows(database, "ium_server", "ium_other", "PUBLIC")))
		run(fixture.Exec("ROLLBACK", 0))
		held(visible)
		// Leave exactly one independently named dependency so the complete
		// CASCADE notice does not rely on allocator-dependent dependency order.
		run(fixture.Exec("DROP USER MAPPING FOR ium_mapped SERVER ium_server", 0))
		run(fixture.Exec("DROP USER MAPPING FOR ium_other SERVER ium_server", 0))
		*notices = nil
		run(fixture.Exec("DROP SERVER ium_server CASCADE", 0))
		iumCascadeNotice(t, b.PG, *notices, "ium_server", "public")
		held(empty)
	})
}
