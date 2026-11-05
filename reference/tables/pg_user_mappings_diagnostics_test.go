package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGUserMappingsDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, pgUserMappingsRoles)
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *fdwSQLReader, run func(fixture.Step)) {
		pgUserMappingsSetup(t, c, run)
		pgUserMappingsMissingColumn(run)
		pgUserMappingsSeed(run, true)
		rows := pgUserMappingsRows(t, p, c, pgUserMappingsSpecs(true))
		check := func() { pgUserMappingsRoutes(t, p, b, c, nil, rows, "fdw_server") }
		for _, failure := range []struct{ q, code, message string }{
			{"CREATE USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(password 'attempted-secret')", "42710", `user mapping for "umv_alice" already exists for server "fdw_server"`},
			{"ALTER USER MAPPING FOR umv_denied SERVER fdw_server OPTIONS(ADD password 'attempted-secret')", "42704", `user mapping for "umv_denied" does not exist for server "fdw_server"`},
			{"DROP USER MAPPING FOR umv_denied SERVER fdw_server", "42704", `user mapping for "umv_denied" does not exist for server "fdw_server"`},
			{"CREATE USER MAPPING FOR umv_denied SERVER fdw_server OPTIONS(password 'attempted-secret',password 'second-secret')", "42710", `option "password" provided more than once`},
			{"ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(ADD password 'attempted-secret')", "42710", `option "password" provided more than once`},
			{"ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET absent 'attempted-secret')", "42704", `option "absent" not found`},
			{"ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(DROP absent)", "42704", `option "absent" not found`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(failure.q, fixture.ControlsReferenceDiagnostic(failure.code, failure.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				check()
			}
		}
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO umv_usage_holder", 0))
		for _, failure := range []struct{ actor, q, message string }{
			{"umv_alice", "ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'attempted-secret')", "permission denied for foreign server fdw_server"},
			{"umv_usage_holder", "ALTER USER MAPPING FOR umv_bob SERVER fdw_server OPTIONS(SET password 'attempted-secret')", "must be owner of foreign server fdw_server"},
			{"umv_usage_holder", "DROP USER MAPPING FOR umv_bob SERVER fdw_server", "must be owner of foreign server fdw_server"},
			{"umv_usage_holder", "ALTER USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(SET password 'attempted-secret')", "must be owner of foreign server fdw_server"},
			{"umv_usage_holder", "DROP USER MAPPING FOR PUBLIC SERVER fdw_server", "must be owner of foreign server fdw_server"},
		} {
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("SET SESSION AUTHORIZATION "+failure.actor, 0))
				s := fixture.ErrorDiagnostic(failure.q, fixture.ControlsReferenceDiagnostic("42501", failure.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				pgUserMappingsRoutes(t, p, b, c, nil, pgUserMappingsMask(rows), "fdw_server")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				check()
			}
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION umv_denied", 0))
		pgUserMappingsRoutes(t, p, b, c, nil, pgUserMappingsMask(rows), "fdw_server")
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_user_mapping", "SELECT * FROM pg_catalog.pg_user_mapping WHERE FALSE"} {
			for _, prepared := range []bool{false, true} {
				s := pgUserMappingDenied(q)
				s.Prepared = prepared
				run(s)
				pgUserMappingsRoutes(t, p, b, c, nil, pgUserMappingsMask(rows), "fdw_server")
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		check()
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, tc := range []struct{ q, code, message string }{
			{"DROP USER MAPPING IF EXISTS FOR umv_denied SERVER fdw_server", "00000", `user mapping for "umv_denied" does not exist for server "fdw_server", skipping`},
			{"CREATE USER MAPPING IF NOT EXISTS FOR umv_alice SERVER fdw_server OPTIONS(password 'attempted-secret')", "42710", `user mapping for "umv_alice" already exists for server "fdw_server", skipping`},
		} {
			for _, prepared := range []bool{false, true} {
				notices = nil
				s := fixture.Exec(tc.q, 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, noticeConn, recorder, s)
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: tc.code, Message: tc.message}
				if len(notices) != 1 || notices[0] != want {
					t.Fatal("mapping notice", notices, "want", want)
				}
				check()
			}
		}
	})
}
