package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsDatabaseIdentity(t *testing.T) {
	const actor = "pgss_database_identity_actor"
	const first = "pgss_database_identity_a"
	const second = "pgss_database_identity_b"
	const renamedA = "pgss_database_identity_renamed_a"
	const renamedB = "pgss_database_identity_renamed_b"
	fixture.RoleReferenceCleanup(t, []string{actor})
	names := []string{first, second, renamedA, renamedB}
	fixture.DatabaseReferenceCleanup(t, &names)
	pgssCase(t, true, func(s *pgssScenario) {
		// Fresh principal identity isolates retained cluster history without
		// resetting it. Setup/observation never contributes to the measured key.
		s.exec("CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-attributes-fixture'")
		for _, setting := range []string{"track='none'", "track_utility=off", "track_planning=off"} {
			s.exec("ALTER ROLE " + actor + " SET pg_stat_statements." + setting)
		}
		databaseOIDs := make(map[string]int64)
		for _, name := range []string{first, second} {
			s.exec("CREATE DATABASE " + name)
			var oid int64
			if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&oid); err != nil {
				s.t.Fatal(name, err)
			}
			databaseOIDs[name] = oid
		}
		var notices []fixture.Diagnostic
		_, worker, recorder := fixture.NoticeReferencePool(t, s.p, s.b.PG, &notices, fixture.NoticeReferenceOption{AuthenticatedRole: actor})
		work := func(step fixture.Step) {
			s.t.Helper()
			fixture.RunStep(s.t, s.p.Ctx, worker, recorder, step)
		}
		work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		work(fixture.Exec("SET pg_stat_statements.track='top'", 0))

		const prefix = "ALTER DATABASE /* pgss database 雪 */ "
		key := " AND userid=(SELECT oid FROM pg_roles WHERE rolname='" + actor + "') AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
		census := func(groups, calls int64) {
			s.t.Helper()
			s.both(fixture.Select("SELECT count(*) AS groups,count(DISTINCT queryid) AS identities,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1"+key,
				[]string{"groups", "identities", "calls", "rows"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{groups, groups, calls, int64(0)}}, prefix+"%"))
		}
		entry := func(query string, calls int64) {
			s.t.Helper()
			s.both(fixture.Select("SELECT query,queryid<>0 AS identified,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query=$1"+key,
				[]string{"query", "identified", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "BOOL", "INT8", "INT8"}, [][]any{{query, true, true, calls, int64(0)}}, query))
		}
		absent := func(query string) {
			s.t.Helper()
			s.both(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE query=$1"+key,
				[]string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, query))
		}
		census(0, 0)
		failed := prefix + first + " RENAME TO " + second
		duplicate := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P04", Message: "database \"" + second + "\" already exists"}
		work(fixture.ErrorDiagnostic(failed, duplicate))
		work(fixture.Prepared(fixture.ErrorDiagnostic(failed, duplicate)))
		absent(failed)
		census(0, 0)
		invalidLimit := prefix + first + " CONNECTION LIMIT '3'"
		integerRequired := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "connection_limit requires an integer value"}
		work(fixture.ErrorDiagnostic(invalidLimit, integerRequired))
		work(fixture.Prepared(fixture.ErrorDiagnostic(invalidLimit, integerRequired)))
		func() {
			statement, err := worker.PrepareContext(s.p.Ctx, invalidLimit)
			if err != nil {
				s.t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: worker, Statement: statement, SQL: invalidLimit}
			absent(invalidLimit)
			census(0, 0)
			for range 2 {
				fixture.RunStep(s.t, s.p.Ctx, held, recorder, fixture.ErrorDiagnostic(invalidLimit, integerRequired))
				absent(invalidLimit)
				census(0, 0)
			}
		}()

		// Released RenameStmt selects both names; AlterDatabaseStmt selects
		// its ordered DefElem list and raw literal kind/value, not just the
		// resulting catalog effect. Refresh is a distinct node with dbname.
		// The dedicated and general TABLESPACE spellings are the same raw node.
		cases := []struct {
			body, variant, undo, reset, database, original string
			limit                                          int64
			batch, refresh, tablespace                     bool
		}{
			{
				body: first + " RENAME TO " + renamedA, variant: "alter database \"" + first + "\" rename to \"" + renamedA + "\"",
				undo: "ALTER DATABASE " + renamedA + " RENAME TO " + first, database: renamedA, limit: -1,
			},
			{
				body: second + " RENAME TO " + renamedA, variant: "alter database \"" + second + "\" rename to \"" + renamedA + "\"",
				undo: "ALTER DATABASE " + renamedA + " RENAME TO " + second, database: renamedA, original: second, limit: -1,
			},
			{
				body: first + " RENAME TO " + renamedB, variant: "alter database \"" + first + "\" rename to \"" + renamedB + "\"",
				undo: "ALTER DATABASE " + renamedB + " RENAME TO " + first, database: renamedB, limit: -1,
			},
			{
				body: first + " CONNECTION LIMIT 3", variant: "alter database \"" + first + "\" with connection_limit=3",
				reset: "ALTER DATABASE " + first + " CONNECTION LIMIT 2", limit: 3, batch: true,
			},
			{
				body: first + " CONNECTION LIMIT 4", variant: "alter database \"" + first + "\" with connection_limit=4",
				reset: "ALTER DATABASE " + first + " CONNECTION LIMIT 2", limit: 4,
			},
			{
				body: first + " ALLOW_CONNECTIONS 1", variant: "alter database \"" + first + "\" with allow_connections=1",
				reset: "ALTER DATABASE " + first + " ALLOW_CONNECTIONS false", limit: 4,
			},
			{
				body: first + " ALLOW_CONNECTIONS true", variant: "alter database \"" + first + "\" with allow_connections='true'",
				reset: "ALTER DATABASE " + first + " ALLOW_CONNECTIONS false", limit: 4,
			},
			{
				body: first + " IS_TEMPLATE false ALLOW_CONNECTIONS true", variant: "alter database \"" + first + "\" with is_template='false' allow_connections='true'",
				reset: "ALTER DATABASE " + first + " ALLOW_CONNECTIONS false", limit: 4,
			},
			{
				body: first + " ALLOW_CONNECTIONS true IS_TEMPLATE false", variant: "alter database \"" + first + "\" with allow_connections='true' is_template='false'",
				reset: "ALTER DATABASE " + first + " ALLOW_CONNECTIONS false", limit: 4,
			},
			{
				body: first + " SET TABLESPACE pg_default", variant: "alter database \"" + first + "\" with tablespace='pg_default'",
				limit: 4, tablespace: true,
			},
			{
				body: first + " REFRESH COLLATION VERSION", variant: "alter database \"" + first + "\" refresh collation version",
				limit: 4, refresh: true,
			},
			{
				body: second + " REFRESH COLLATION VERSION", variant: "alter database \"" + second + "\" refresh collation version",
				database: second, original: second, limit: -1, refresh: true,
			},
		}
		// REL_18_6 pgss_ProcessUtility saves then permanently zeros the retained
		// PlannedStmt.queryId. A second held Execute still runs but pgss_store
		// rejects its zero saved ID. Native keeps invocation-owned accounting.
		// See docs/POSTGRES_REGRESSION.md and the tests-20 first held-rename proof.
		heldRawUtilityCollectedCalls := int64(2)
		if s.b.PG {
			heldRawUtilityCollectedCalls = 1
		}
		collectedPerCase := 2 + heldRawUtilityCollectedCalls
		for i, tc := range cases {
			func() {
				query := prefix + tc.body
				run := func(executor fixture.Executor, step fixture.Step) {
					s.t.Helper()
					if tc.reset != "" {
						// Reset to a contrasting value before every execution,
						// including the second held one whose PG ID is now zero.
						s.exec(tc.reset)
					}
					notices = nil
					fixture.RunStep(s.t, s.p.Ctx, executor, recorder, step)
					database, original := tc.database, tc.original
					if database == "" {
						database = first
					}
					if original == "" {
						original = first
					}
					s.both(fixture.Select("SELECT oid::bigint AS oid,datname,datistemplate,datallowconn,datconnlimit,datcollversion FROM pg_database WHERE datname=$1",
						[]string{"oid", "datname", "datistemplate", "datallowconn", "datconnlimit", "datcollversion"},
						[]string{"INT8", "NAME", "BOOL", "BOOL", "INT4", "TEXT"},
						[][]any{{databaseOIDs[original], database, false, true, tc.limit, nil}}, database))
					if tc.tablespace {
						// Native's documented logical default is NULL; the
						// genuine PG physical default tablespace is OID1663.
						var placement any
						if s.b.PG {
							placement = int64(1663)
						}
						s.both(fixture.Select("SELECT dattablespace::bigint AS dattablespace FROM pg_database WHERE datname=$1",
							[]string{"dattablespace"}, []string{"INT8"}, [][]any{{placement}}, database))
					}
					if tc.refresh {
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version has not changed"}
						if len(notices) != 1 || notices[0] != want {
							s.t.Fatalf("actual refresh execution notices: %+v, want %+v", notices, want)
						}
					}
					if tc.undo != "" {
						s.both(fixture.Select("SELECT count(*) AS n FROM pg_database WHERE datname=$1",
							[]string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, original))
						// An unmeasured principal restores the old name so the
						// same prepared/held SQL can genuinely execute again.
						s.exec(tc.undo)
					}
				}
				groups, base := int64(i+1), collectedPerCase*int64(i)
				direct := query
				if tc.batch {
					// Correct top-level Pos keeps the preceding SET (including
					// its UTF8 literal) out of this first representative.
					direct = "SET application_name='pgss database batch 雪'; " + query
				}
				run(worker, fixture.Exec(direct, 0))
				entry(query, 1)
				census(groups, base+1)
				run(worker, fixture.Prepared(fixture.Exec(tc.variant, 0)))
				entry(query, 2)
				absent(tc.variant)
				census(groups, base+2)

				statement, err := worker.PrepareContext(s.p.Ctx, tc.variant)
				if err != nil {
					s.t.Fatal(err)
				}
				defer statement.Close()
				held := fixture.HeldStatement{Executor: worker, Statement: statement, SQL: tc.variant}
				// Parse/Describe/retention is not utility execution. Both held
				// executions below must independently change/assert the catalog;
				// only PG's retained-plan collector stops after the first one.
				entry(query, 2)
				census(groups, base+2)
				for execution := int64(1); execution <= 2; execution++ {
					run(held, fixture.Exec(tc.variant, 0))
					collected := 2 + min(execution, heldRawUtilityCollectedCalls)
					entry(query, collected)
					absent(tc.variant)
					census(groups, base+collected)
				}
			}()
		}
		work(fixture.ErrorDiagnostic(failed, duplicate))
		absent(failed)
		absent(invalidLimit)
		census(int64(len(cases)), collectedPerCase*int64(len(cases)))
	})
}
