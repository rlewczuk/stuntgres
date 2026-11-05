package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgUserMappingsProbes(run func(fixture.Step), rows [][]any) {
	first := map[string]string{"umv_alice": "password=alice-secret", "umv_bob": "password=bob-secret", "public": "password=public-secret", "umv_owner": "password=owner-secret"}
	projected, filtered := make([][]any, 0, len(rows)), make([][]any, 0)
	for _, row := range rows {
		var element any
		if row[5] != nil {
			element = first[row[4].(string)]
			filtered = append(filtered, []any{row[4], row[5]})
		}
		projected = append(projected, []any{row[4], row[5], element, row[5], row[5] == nil})
	}
	pgUserMappingBoth(run, fixture.Select("SELECT usename,umoptions,umoptions[1] AS first,umoptions::text AS as_text,umoptions IS NULL AS hidden FROM pg_user_mappings WHERE srvname='fdw_server' ORDER BY usename", []string{"usename", "umoptions", "first", "as_text", "hidden"}, []string{"NAME", "_TEXT", "TEXT", "TEXT", "BOOL"}, projected))
	pgUserMappingBoth(run, fixture.Select("SELECT usename,umoptions FROM pg_user_mappings WHERE srvname='fdw_server' AND umoptions IS NOT NULL ORDER BY usename", []string{"usename", "umoptions"}, []string{"NAME", "_TEXT"}, filtered))
}

// Every case starts from isolated server grants. Membership in a mapped role
// never substitutes for equality with current_user.
func pgUserMappingsMatrix(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), rows [][]any, held *fixture.HeldStatement) {
	t.Helper()
	check := func(allowed ...string) {
		want := pgUserMappingsMask(rows, allowed...)
		pgUserMappingsRoutes(t, p, b, c, held, want, "fdw_server")
		pgUserMappingsProbes(run, want)
	}
	actor := func(role string, allowed ...string) {
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		check(allowed...)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}
	check("all")
	actor("umv_alice")
	run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_alice", 0))
	actor("umv_alice", "umv_alice")
	run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM umv_alice", 0))
	run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_usage_holder", 0))
	run(fixture.Exec("GRANT umv_usage_holder TO umv_alice WITH INHERIT TRUE, SET TRUE", 0))
	actor("umv_alice", "umv_alice")
	run(fixture.Exec("REVOKE umv_usage_holder FROM umv_alice", 0))
	actor("umv_usage_holder")
	run(fixture.Exec("GRANT umv_usage_holder TO umv_usage_member WITH INHERIT TRUE, SET TRUE", 0))
	actor("umv_usage_member")
	run(fixture.Exec("REVOKE umv_usage_holder FROM umv_usage_member", 0))
	run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM umv_usage_holder", 0))
	run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO PUBLIC", 0))
	actor("umv_bob", "umv_bob")
	run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM PUBLIC", 0))
	actor("umv_bob")
	actor("umv_denied")
	actor("umv_owner", "public", "umv_owner")
	run(fixture.Exec("GRANT umv_owner TO umv_owner_member WITH INHERIT TRUE, SET TRUE", 0))
	actor("umv_owner_member", "public")
	run(fixture.Exec("REVOKE umv_owner FROM umv_owner_member", 0))
	run(fixture.Exec("GRANT umv_owner TO umv_owner_set WITH INHERIT FALSE, SET TRUE", 0))
	actor("umv_owner_set")
	run(fixture.Exec("SET SESSION AUTHORIZATION umv_owner_set", 0))
	run(fixture.Exec("SET ROLE umv_owner", 0))
	check("public", "umv_owner")
	run(fixture.Exec("RESET ROLE", 0))
	check()
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	run(fixture.Exec("REVOKE umv_owner FROM umv_owner_set", 0))
	run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_alice", 0))
	run(fixture.Exec("GRANT umv_alice TO umv_alice_member WITH INHERIT TRUE, SET TRUE", 0))
	actor("umv_alice_member")
	run(fixture.Exec("SET SESSION AUTHORIZATION umv_alice_member", 0))
	run(fixture.Exec("SET ROLE umv_alice", 0))
	check("umv_alice")
	run(fixture.Exec("RESET ROLE", 0))
	check()
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	run(fixture.Exec("REVOKE umv_alice FROM umv_alice_member", 0))
	run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM umv_alice", 0))
	// Changing ownership is separate from USAGE; neither former owner nor new
	// owner gets access to another named principal's options.
	run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO umv_alice", 0))
	run(fixture.Exec("ALTER SERVER fdw_server OWNER TO umv_alice", 0))
	run(fixture.Exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdw_meta FROM umv_alice", 0))
	actor("umv_alice", "umv_alice", "public")
	actor("umv_owner")
	run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO umv_owner", 0))
	run(fixture.Exec("ALTER SERVER fdw_server OWNER TO umv_owner", 0))
	run(fixture.Exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdw_meta FROM umv_owner", 0))
	check("all")
}

func TestReferencePGUserMappingsVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, pgUserMappingsRoles)
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		before := reader.snapshot()
		pgUserMappingsSetup(t, c, run)
		pgUserMappingsSeed(run, false)
		held := pgUserMappingsHold(t, p, c)
		rows := pgUserMappingsRows(t, p, c, pgUserMappingsSpecs(false))
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_owner", 0))
		pgUserMappingsRoutes(t, p, b, c, &held, pgUserMappingsMask(rows, "public"), "fdw_server")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR umv_owner SERVER fdw_server OPTIONS(password 'owner-secret')", 0))
		rows = pgUserMappingsRows(t, p, c, pgUserMappingsSpecs(true))
		pgUserMappingsMatrix(t, p, b, c, run, rows, &held)
		if !b.PG && reader.snapshot() != before {
			t.Fatal("authority matrix invoked foreign provider")
		}
	})
}
