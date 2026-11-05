package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGUserMappingsLifecycle(t *testing.T) {
	for _, route := range []struct {
		name     string
		prepared bool
	}{{"direct", false}, {"prepared", true}} {
		t.Run(route.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, pgUserMappingsRoles)
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *fdwSQLReader, run func(fixture.Step)) {
				pgUserMappingsSetup(t, c, run)
				exec := func(q string) { s := fixture.Exec(q, 0); s.Prepared = route.prepared; run(s) }
				held := pgUserMappingsHold(t, p, c)
				check := func(rows [][]any) { pgUserMappingsRoutes(t, p, b, c, &held, rows, "fdw_server") }
				check(nil)
				exec("CREATE USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(password 'one')")
				rows := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"fdw_server", "umv_alice", "{password=one}"}})
				check(rows)
				original := rows[0][0]
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(ADD empty '',ADD tag 'é=🙂')")
				rows[0][5] = "{password=one,empty=,tag=é=🙂}"
				check(rows)
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'two',DROP empty)")
				rows[0][5] = "{password=two,tag=é=🙂}"
				check(rows)
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(DROP password,DROP tag)")
				rows[0][5] = nil
				check(rows)
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(ADD password 'stable')")
				rows[0][5] = "{password=stable}"
				check(rows)
				run(fixture.Exec("BEGIN", 0))
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'inside')")
				inside := pgUserMappingsMask(rows, "all")
				inside[0][5] = "{password=inside}"
				check(inside)
				run(fixture.Exec("ROLLBACK", 0))
				check(rows)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT mapping_save", 0))
				exec("DROP USER MAPPING FOR umv_alice SERVER fdw_server")
				check(nil)
				run(fixture.Exec("ROLLBACK TO mapping_save", 0))
				check(rows)
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'discarded')")
				run(fixture.Exec("ROLLBACK TO mapping_save", 0))
				check(rows)
				exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'committed')")
				run(fixture.Exec("COMMIT", 0))
				rows[0][5] = "{password=committed}"
				check(rows)
				failure := fixture.ErrorDiagnostic("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'must-not-publish',DROP absent)", fixture.ControlsReferenceDiagnostic("42704", `option "absent" not found`))
				failure.Prepared, failure.ErrorStage = route.prepared, "execute"
				run(failure)
				check(rows)
				exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(password 'public-secret')")
				both := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"fdw_server", "umv_alice", "{password=committed}"}, {"fdw_server", "", pgUserMappingsPublic}})
				check(both)
				run(fixture.Exec("BEGIN", 0))
				exec("DROP USER MAPPING FOR umv_alice SERVER fdw_server")
				check(both[:1])
				run(fixture.Exec("ROLLBACK", 0))
				check(both)
				exec("DROP USER MAPPING FOR umv_alice SERVER fdw_server")
				check(both[:1])
				exec("CREATE USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(password 'recreated')")
				recreated := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"fdw_server", "umv_alice", "{password=recreated}"}, {"fdw_server", "", pgUserMappingsPublic}})
				if recreated[1][0] == original {
					t.Fatal("recreated mapping reused dropped identity", original)
				}
				check(recreated)
				exec("DROP USER MAPPING FOR PUBLIC SERVER fdw_server")
				check(recreated[1:])
				exec("CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_meta")
				exec("CREATE USER MAPPING FOR umv_bob SERVER fdw_second OPTIONS(password 'second')")
				exec("CREATE FOREIGN DATA WRAPPER fdw_bad NO HANDLER NO VALIDATOR")
				exec("CREATE SERVER umv_independent FOREIGN DATA WRAPPER fdw_bad")
				exec("CREATE USER MAPPING FOR PUBLIC SERVER umv_independent OPTIONS(password 'independent')")
				second := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"fdw_second", "umv_bob", "{password=second}"}})
				independent := pgUserMappingsRows(t, p, c, []pgUserMappingExpected{{"umv_independent", "", "{password=independent}"}})
				exec("DROP SERVER fdw_server CASCADE")
				check(nil)
				pgUserMappingsRoutes(t, p, b, c, &held, second, "fdw_second")
				pgUserMappingsRoutes(t, p, b, c, &held, independent, "umv_independent")
				exec("DROP FOREIGN DATA WRAPPER fdw_meta CASCADE")
				pgUserMappingsRoutes(t, p, b, c, &held, nil, "fdw_second")
				pgUserMappingsRoutes(t, p, b, c, &held, independent, "umv_independent")
			})
		})
	}
}
