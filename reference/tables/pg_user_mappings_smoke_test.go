package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGUserMappingsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, pgUserMappingsRoles)
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		before := reader.snapshot()
		pgUserMappingsSetup(t, c, run)
		for _, q := range []string{
			"CREATE USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(password 'alice-secret',empty '',tag 'é=🙂')",
			"CREATE USER MAPPING FOR umv_bob SERVER fdw_server OPTIONS(password 'bob-secret')",
			"CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(password 'public-secret')",
		} {
			run(fixture.Exec(q, 0))
		}
		specs := []pgUserMappingExpected{{"fdw_server", "umv_alice", pgUserMappingsAlice}, {"fdw_server", "umv_bob", pgUserMappingsBob}, {"fdw_server", "", pgUserMappingsPublic}}
		rows := pgUserMappingsRows(t, p, c, specs)
		held := pgUserMappingsHold(t, p, c)
		pgUserMappingsRoutes(t, p, b, c, &held, rows, "fdw_server")
		shape, err := c.QueryContext(p.Ctx, pgUserMappingsQuery, "fdw_server")
		if err != nil {
			t.Fatal(err)
		}
		pgUserMappingsMetadata(t, shape, b.PG)
		pgUserMappingsConsume(t, b, shape, rows)
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminExec := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_alice")
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_alice", 0))
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, "umv_alice"), "fdw_server")
		adminExec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM umv_alice")
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows), "fdw_server")
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_alice")
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, "umv_alice"), "fdw_server")
		adminExec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'changed=é')")
		rows[1][5] = "{password=changed=é,empty=,tag=é=🙂}"
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, "umv_alice"), "fdw_server")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, actor := range []string{"umv_denied", "umv_owner"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
			var allowed []string
			if actor == "umv_owner" {
				allowed = []string{"public"}
			}
			pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, allowed...), "fdw_server")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		pgUserMappingsMissingColumn(run)
		pgUserMappingsRoutes(t, p, b, c, &held, rows, "fdw_server")
		run(fixture.Exec("DROP USER MAPPING FOR umv_alice SERVER fdw_server", 0))
		remaining := [][]any{rows[0], rows[2]}
		pgUserMappingsRoutes(t, p, b, c, &held, remaining, "fdw_server")
		if !b.PG && reader.snapshot() != before {
			t.Fatal("handlerless metadata smoke invoked foreign provider")
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgUserMappingsRoutes(t, p, b, c, &held, nil, "fdw_server")
		empty, err := c.QueryContext(p.Ctx, pgUserMappingsQuery, "fdw_server")
		if err != nil {
			t.Fatal(err)
		}
		pgUserMappingsMetadata(t, empty, false)
		pgUserMappingsConsume(t, b, empty, nil)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		restored := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"fdw_server", "umv_bob", pgUserMappingsBob}, {"fdw_server", "", pgUserMappingsPublic}})
		pgUserMappingsRoutes(t, p, b, c, &held, restored, "fdw_server")
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_denied", 0))
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(restored), "fdw_server")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
