package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWExplainAnalyzePermissions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		want := fdwAdvancedLive(t, p, b, r, run)
		for _, role := range []string{"fdw_owner", "fdw_other", "fdw_reader"} {
			run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'fdw_password'", 0))
		}
		if !b.PG {
			fdwSourceExec(t, p, b, r, run, "CREATE ROLE fdw_reader LOGIN PASSWORD 'fdw_password'", 0)
		}
		fdwSourceExec(t, p, b, r, run, "GRANT USAGE ON SCHEMA fdw_source TO fdw_reader", 0)
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_owner,fdw_other,fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_owner,fdw_other", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data OWNER TO fdw_owner", 0))
		run(fixture.Exec("GRANT MAINTAIN,SELECT ON fdw_data TO fdw_other", 0))
		valid, denied := "user 'postgres'", "user 'fdw_reader'"
		if b.PG {
			valid = "user 'stuntgres',password 'stuntgres',password_required 'false'"
			denied = "user 'fdw_reader',password 'fdw_password',password_required 'false'"
		}
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+valid+")", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_other SERVER fdw_live OPTIONS("+denied+")", 0))
		run(fixture.Exec("SET ROLE fdw_other", 0))
		// MAINTAIN actor's deliberately denied remote mapping is irrelevant.
		run(fixture.Exec("ANALYZE fdw_data", 0))
		fdwPopulation(run, 20)
		run(fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			r.advanced.mu.Lock()
			request := r.advanced.requests[len(r.advanced.requests)-1]
			r.advanced.mu.Unlock()
			if request.Role != "fdw_owner" {
				t.Fatal("sampling used actor mapping", request.Role)
			}
			run(fixture.Exec("SET ROLE fdw_other", 0))
			fdwGatedInformation(t, p, b, c, r, "AcquireSampleRows", "revoke", nil)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT MAINTAIN ON fdw_data TO fdw_other", 0))
			fdwPopulation(run, 20)
		}
		fdwAnalyzeNotices(t, p, b, r, run)
		// Local SELECT denial must precede every provider planning callback.
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		before := r.snapshot()
		step := fixture.ErrorDiagnostic("EXPLAIN (VERBOSE) SELECT * FROM fdw_data", fdwDiagnostic("42501", "permission denied for foreign table fdw_data"))
		run(step)
		run(fixture.Prepared(step))
		if !b.PG && r.snapshot() != before {
			t.Fatal("local denial entered provider")
		}
		run(fixture.Exec("RESET ROLE", 0))
		// Actor-only mapping cannot supply missing owner sampling authority.
		run(fixture.Exec("DROP USER MAPPING FOR fdw_owner SERVER fdw_live", 0))
		run(fixture.Exec("SET ROLE fdw_other", 0))
		missing := fixture.ErrorDiagnostic("ANALYZE fdw_data", fdwDiagnostic("42704", `user mapping not found for user "fdw_owner", server "fdw_live"`))
		run(missing)
		run(fixture.Prepared(missing))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_live OPTIONS("+valid+")", 0))
		run(fixture.Exec("SET ROLE fdw_other", 0))
		run(fixture.Prepared(fixture.Exec("ANALYZE fdw_data", 0)))
		run(fixture.Exec("RESET ROLE", 0))
		// Exact owner mapping takes precedence over a valid PUBLIC fallback.
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+denied+")", 0))
		fdwRemoteInformationDenied(t, p, b, c, "ANALYZE fdw_data")
		fdwPopulation(run, 20)
		run(fixture.Exec("DROP USER MAPPING FOR fdw_owner SERVER fdw_live", 0))
		// postgres_fdw only contacts remote during plain EXPLAIN when actual
		// remote estimates are requested. Native planning really counts source.
		if b.PG {
			run(fixture.Exec("ALTER SERVER fdw_live OPTIONS(ADD use_remote_estimate 'true')", 0))
		}
		run(fixture.Exec("SET ROLE fdw_other", 0))
		fdwRemoteInformationDenied(t, p, b, c, "EXPLAIN (VERBOSE) SELECT * FROM fdw_data")
		run(fixture.Exec("RESET ROLE", 0))
		if b.PG {
			run(fixture.Exec("ALTER SERVER fdw_live OPTIONS(DROP use_remote_estimate)", 0))
		}
		fdwWantEstimate(t, p, c, "label='common'", 14)
		fdwAssertSource(t, p, b, r, want)
		fdwMetadata(run)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_unavailable(id integer) SERVER fdw_server", 0))
		for _, query := range []string{"ANALYZE fdw_unavailable", "EXPLAIN SELECT * FROM fdw_unavailable"} {
			step := fixture.ErrorDiagnostic(query, fdwDiagnostic("55000", `foreign-data wrapper "fdw_meta" has no handler`))
			run(step)
			run(fixture.Prepared(step))
		}
	})
}

func fdwRemoteInformationDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var err error
		if prepared {
			statement, prepareErr := c.PrepareContext(p.Ctx, query)
			if prepareErr != nil {
				err = prepareErr
			} else {
				_, err = statement.ExecContext(p.Ctx)
				if closeErr := statement.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
		} else {
			_, err = c.ExecContext(p.Ctx, query)
		}
		want := fdwDiagnostic("42501", "permission denied for table data")
		if b.PG {
			if query == "ANALYZE fdw_data" {
				want.Context = "remote SQL command: DECLARE c1 CURSOR FOR SELECT id, label, flag FROM fdw_source.data"
			} else {
				want.Context = "remote SQL command: EXPLAIN SELECT id, label, flag FROM fdw_source.data"
			}
		}
		fixture.AssertDiagnostic(t, err, &want)
	}
}
