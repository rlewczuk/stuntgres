package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsTablespaceIdentity(t *testing.T) {
	const actor = "pgss_tablespace_identity_actor"
	fixture.RoleReferenceCleanup(t, []string{actor})
	pgssCase(t, true, func(s *pgssScenario) {
		// A new effective-user key isolates first representatives without
		// resetting the cluster history retained across extension installations.
		s.exec("CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-attributes-fixture'")
		s.exec("GRANT USAGE ON SCHEMA pgss_part TO " + actor)
		for _, setting := range []string{"track='none'", "track_utility=off", "track_planning=off"} {
			s.exec("ALTER ROLE " + actor + " SET pg_stat_statements." + setting)
		}
		var notices []fixture.Diagnostic
		_, worker, recorder := fixture.NoticeReferencePool(t, s.p, s.b.PG, &notices, fixture.NoticeReferenceOption{AuthenticatedRole: actor})
		work := func(step fixture.Step) {
			s.t.Helper()
			fixture.RunStep(s.t, s.p.Ctx, worker, recorder, step)
		}
		work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		work(fixture.Exec("SET pg_stat_statements.track='top'", 0))

		key := " AND userid=(SELECT oid FROM pg_roles WHERE rolname='" + actor + "') AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
		census := func(pattern string, groups, calls int64) {
			s.t.Helper()
			s.both(fixture.Select("SELECT count(*) AS groups,count(DISTINCT queryid) AS identities,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1"+key,
				[]string{"groups", "identities", "calls", "rows"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{groups, groups, calls, int64(0)}}, pattern))
		}
		entry := func(query string, calls int64) {
			s.t.Helper()
			s.both(fixture.Select("SELECT query,queryid<>0 AS identified,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query=$1"+key,
				[]string{"query", "identified", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "BOOL", "INT8", "INT8"}, [][]any{{query, true, true, calls, int64(0)}}, query))
		}
		absentRepresentative := func(query string) {
			s.t.Helper()
			s.both(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE query=$1"+key,
				[]string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, query))
		}
		names := func(first, second string) fixture.Step {
			return fixture.Select("SELECT oid::bigint AS oid,spcname FROM pg_tablespace WHERE oid IN (1663,1664) ORDER BY oid",
				[]string{"oid", "spcname"}, []string{"INT8", "NAME"}, [][]any{{int64(1663), first}, {int64(1664), second}})
		}
		s.both(names("pg_default", "pg_global"))
		census("ALTER TABLESPACE %", 0, 0)
		census("DROP TABLESPACE %", 0, 0)

		// REL_18_6 RenameStmt selects subname (the old name) and newname;
		// gram.y supplies OBJECT_TABLESPACE and otherwise zero/nil defaults.
		// The same destination with a different old name and the same old name
		// with a different destination therefore produce three identities.
		// None of these fields supplies a replacement location: raw comments,
		// spelling and identifiers remain in the first representative.
		for i, tc := range []struct{ old, new string }{
			{"pg_default", "pgss_tablespace_identity_a"},
			{"pg_global", "pgss_tablespace_identity_a"},
			{"pg_default", "pgss_tablespace_identity_b"},
		} {
			query := "ALTER TABLESPACE /* pgss tablespace 雪 */ " + tc.old + " RENAME TO " + tc.new
			quoted := "ALTER TABLESPACE \"" + tc.old + "\" RENAME /* quoted */ TO \"" + tc.new + "\""
			for _, prepared := range []bool{false, true} {
				func() {
					// Register before BEGIN. Even a fatal assertion or an expired
					// scenario context must roll back, never commit a builtin rename
					// into the shared oracle. Cleanup has its own bounded context.
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if _, err := worker.ExecContext(ctx, "ROLLBACK"); err != nil {
							s.t.Error("tablespace identity rollback", err)
						}
					}()
					work(fixture.Exec("BEGIN", 0))
					step := fixture.Exec(query, 0)
					if prepared {
						step = fixture.Prepared(fixture.Exec(quoted, 0))
					}
					work(step)
					// Read the genuine transactional catalog on its owning session,
					// with collection disabled for both observation protocols.
					work(fixture.Exec("SET pg_stat_statements.track='none'", 0))
					first, second := "pg_default", "pg_global"
					if tc.old == "pg_default" {
						first = tc.new
					} else {
						second = tc.new
					}
					work(names(first, second))
					work(fixture.Prepared(names(first, second)))
				}()
				// Rollback restores the builtin catalog names but does not erase
				// the successful utility's independently completed statistics.
				s.both(names("pg_default", "pg_global"))
				calls := int64(1)
				if prepared {
					calls = 2
				}
				entry(query, calls)
				absentRepresentative(quoted)
				census("ALTER TABLESPACE %", int64(i+1), int64(2*i)+calls)
			}
		}

		// Released DropTableSpaceStmt selects tablespacename and missing_ok.
		// Exercise its successful IF EXISTS path only for absent names: no
		// physical directories and no attempt to physically DROP a builtin.
		for i, name := range []string{"pgss_tablespace_identity_absent_a", "pgss_tablespace_identity_absent_b"} {
			query := "DROP TABLESPACE IF EXISTS /* pgss absent 雪 */ " + name
			quoted := "DROP TABLESPACE IF EXISTS \"" + name + "\""
			missing := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "tablespace \"" + name + "\" does not exist"}
			for _, prepared := range []bool{false, true} {
				// Without missing_ok this is a genuine failure, with all other
				// diagnostic fields absent, and contributes no successful entry.
				strict := "DROP TABLESPACE " + name
				step := fixture.ErrorDiagnostic(strict, missing)
				step.Prepared = prepared
				notices = nil
				work(step)
				if len(notices) != 0 {
					s.t.Fatalf("strict DROP notices: %+v", notices)
				}
				absentRepresentative(strict)
				if prepared {
					census("DROP TABLESPACE %", int64(i+1), int64(2*i+1))
				} else {
					census("DROP TABLESPACE %", int64(i), int64(2*i))
				}
				if prepared {
					// The preceding direct success remains unchanged by failure.
					entry(query, 1)
				}
				step = fixture.Exec(query, 0)
				if prepared {
					step = fixture.Prepared(fixture.Exec(quoted, 0))
				}
				notices = nil
				work(step)
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "tablespace \"" + name + "\" does not exist, skipping"}
				if len(notices) != 1 || notices[0] != want {
					s.t.Fatalf("absent DROP notices: %+v, want %+v", notices, want)
				}
				calls := int64(1)
				if prepared {
					calls = 2
				}
				entry(query, calls)
				absentRepresentative(quoted)
				census("DROP TABLESPACE %", int64(i+1), int64(2*i)+calls)
				s.both(fixture.Select("SELECT count(*) AS n FROM pg_tablespace WHERE spcname=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, name))
				s.both(names("pg_default", "pg_global"))
			}
		}
		census("ALTER TABLESPACE %", 3, 6)
		census("DROP TABLESPACE %", 2, 4)
	})
}
