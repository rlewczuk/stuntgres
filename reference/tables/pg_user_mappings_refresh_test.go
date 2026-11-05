package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGUserMappingsRefresh(t *testing.T) {
	fixture.RoleReferenceCleanup(t, append(append([]string(nil), pgUserMappingsRoles...), "umv_renamed"))
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *fdwSQLReader, run func(fixture.Step)) {
		pgUserMappingsSetup(t, c, run)
		pgUserMappingsSeed(run, true)
		run(fixture.Exec("CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_meta", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR umv_alice SERVER fdw_second OPTIONS(password 'second-secret')", 0))
		rows := pgUserMappingsRows(t, p, c, pgUserMappingsSpecs(true))
		second := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"fdw_second", "umv_alice", "{password=second-secret}"}})
		held := pgUserMappingsHold(t, p, c)
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminExec := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
		check := func(allowed ...string) {
			pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, allowed...), "fdw_server")
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_alice", 0))
		check()
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_alice")
		check("umv_alice")
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(second), "fdw_second")
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_second TO umv_alice")
		pgUserMappingsRoutes(t, p, b, c, &held, second, "fdw_second")
		check("umv_alice")
		adminExec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM umv_alice")
		check()
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_usage_holder")
		adminExec("GRANT umv_usage_holder TO umv_alice WITH INHERIT TRUE, SET TRUE")
		check("umv_alice")
		adminExec("REVOKE umv_usage_holder FROM umv_alice")
		check()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		check("all")
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_owner_set", 0))
		check()
		adminExec("GRANT umv_owner TO umv_owner_set WITH INHERIT FALSE, SET TRUE")
		check()
		run(fixture.Exec("SET ROLE umv_owner", 0))
		check("public", "umv_owner")
		run(fixture.Exec("RESET ROLE", 0))
		check()
		adminExec("REVOKE umv_owner FROM umv_owner_set")
		adminExec("GRANT umv_owner TO umv_owner_set WITH INHERIT TRUE, SET TRUE")
		check("public")
		adminExec("REVOKE umv_owner FROM umv_owner_set")
		check()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		adminExec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO umv_alice")
		adminExec("ALTER SERVER fdw_server OWNER TO umv_alice")
		adminExec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdw_meta FROM umv_alice")
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_alice", 0))
		check("umv_alice", "public")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_owner", 0))
		check()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		adminExec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO umv_owner")
		adminExec("ALTER SERVER fdw_server OWNER TO umv_owner")
		adminExec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdw_meta FROM umv_owner")
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_alice")
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_alice", 0))
		oldWant := pgUserMappingsMask(rows, "umv_alice")
		var old *sql.Rows
		if !b.PG {
			old, err = held.QueryContext(p.Ctx, pgUserMappingsQuery, "fdw_server")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
		}
		adminExec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'changed=é')")
		adminExec("ALTER SERVER fdw_server RENAME TO fdw_renamed")
		// Use a separate administrator: PostgreSQL disallows renaming one's own
		// current/session role from that role's connection.
		adminExec("ALTER ROLE umv_alice RENAME TO umv_renamed")
		adminExec("REVOKE USAGE ON FOREIGN SERVER fdw_renamed FROM umv_renamed")
		if old != nil {
			pgUserMappingsConsume(t, b, old, oldWant)
		}
		for _, row := range rows {
			row[2] = "fdw_renamed"
			if row[4] == "umv_alice" {
				row[4], row[5] = "umv_renamed", "{password=changed=é,empty=,tag=é=🙂}"
			}
		}
		// Renaming changes lexical order; capture no new identities or options.
		// public,bob,empty,owner,renamed is the prescribed new order.
		rows = [][]any{rows[0], rows[2], rows[3], rows[4], rows[1]}
		pgUserMappingsRoutes(t, p, b, c, &held, nil, "fdw_server")
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows), "fdw_renamed")
		var deniedOld *sql.Rows
		if !b.PG {
			deniedOld, err = held.QueryContext(p.Ctx, pgUserMappingsQuery, "fdw_renamed")
			if err != nil {
				t.Fatal(err)
			}
			defer deniedOld.Close()
		}
		adminExec("GRANT USAGE ON FOREIGN SERVER fdw_renamed TO umv_renamed")
		if deniedOld != nil {
			pgUserMappingsConsume(t, b, deniedOld, pgUserMappingsMask(rows))
		}
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, "umv_renamed"), "fdw_renamed")
		second[0][4] = "umv_renamed"
		pgUserMappingsRoutes(t, p, b, c, &held, second, "fdw_second")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		pgUserMappingsRoutes(t, p, b, c, &held, rows, "fdw_renamed")
		// Privileged relationships still resolve to the original generated IDs.
		fresh := pgUserMappingsRows(t, p, admin, []pgUserMappingExpected{{"fdw_renamed", "umv_renamed", "{password=changed=é,empty=,tag=é=🙂}"}, {"fdw_renamed", "umv_bob", pgUserMappingsBob}, {"fdw_renamed", "", pgUserMappingsPublic}, {"fdw_renamed", "umv_empty_user", nil}, {"fdw_renamed", "umv_owner", pgUserMappingsOwner}})
		if _, ok := fixture.CompareRows(fresh, rows, false); !ok {
			t.Fatal("rename changed identities", fresh, rows)
		}
	})
}
