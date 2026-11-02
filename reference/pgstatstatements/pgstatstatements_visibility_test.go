package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_visibility_a", "pgss_visibility_b", "pgss_visibility_group"})
	pgssCase(t, true, func(s *pgssScenario) {
		for _, name := range []string{"pgss_visibility_a", "pgss_visibility_b", "pgss_visibility_group"} {
			s.exec("CREATE ROLE " + name)
		}
		s.exec("GRANT USAGE ON SCHEMA pgss_part TO pgss_visibility_a,pgss_visibility_b")
		s.exec("CREATE TABLE pgss_part.pgss_visibility_secret(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_visibility_secret VALUES(41)", 1))
		s.exec("GRANT SELECT ON pgss_part.pgss_visibility_secret TO pgss_visibility_a,pgss_visibility_b")
		q := "SELECT k AS pgss_visible FROM pgss_part.pgss_visibility_secret"
		for _, name := range []string{"pgss_visibility_a", "pgss_visibility_b"} {
			s.work(fixture.Exec("SET ROLE "+name, 0))
			s.work(fixture.Select(q, []string{"pgss_visible"}, []string{"INT4"}, [][]any{{int64(41)}}))
			s.work(fixture.Exec("RESET ROLE", 0))
		}
		defer s.worker.ExecContext(s.p.Ctx, "RESET ROLE")
		read := "SELECT r.rolname::text AS role,s.queryid IS NOT NULL AS identified,s.query,s.calls,s.rows FROM pgss_part.pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('pgss_visibility_a','pgss_visibility_b') AND s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY r.rolname"
		names := []string{"role", "identified", "query", "calls", "rows"}
		types := []string{"TEXT", "BOOL", "TEXT", "INT8", "INT8"}
		visible := [][]any{{"pgss_visibility_a", true, q, int64(1), int64(1)}, {"pgss_visibility_b", true, q, int64(1), int64(1)}}
		s.both(fixture.Select(read, names, types, visible))
		s.both(fixture.Select("SELECT count(DISTINCT queryid) AS n FROM pgss_part.pg_stat_statements WHERE userid IN (SELECT oid FROM pg_roles WHERE rolname IN ('pgss_visibility_a','pgss_visibility_b')) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.exec("SET ROLE pgss_visibility_b")
		defer s.observer.ExecContext(s.p.Ctx, "RESET ROLE")
		held, err := s.observer.PrepareContext(s.p.Ctx, read)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		denied := [][]any{{"pgss_visibility_a", false, "<insufficient privilege>", int64(1), int64(1)}, {"pgss_visibility_b", true, q, int64(1), int64(1)}}
		check := func(want [][]any) {
			s.both(fixture.Select(read, names, types, want))
			fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.observer, Statement: held, SQL: read}, s.b.Recorder, fixture.Select(read, names, types, want))
		}
		check(denied)
		// showtext=false suppresses even authorized own text without hiding
		// counters; forbidden IDs stay NULL independently of showtext.
		s.both(fixture.Select("SELECT r.rolname::text AS role,s.queryid IS NOT NULL AS identified,s.query,s.calls,s.rows FROM pgss_part.pg_stat_statements(false) s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('pgss_visibility_a','pgss_visibility_b') AND s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY r.rolname", names, types, [][]any{{"pgss_visibility_a", false, nil, int64(1), int64(1)}, {"pgss_visibility_b", true, nil, int64(1), int64(1)}}))
		s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)", pgssNames, pgssTypes, nil))
		// Authority changes occur on the other session. Retained prepared
		// readers must re-evaluate inherited authority at every execution.
		s.work(fixture.Exec("GRANT pg_read_all_stats TO pgss_visibility_group", 0))
		s.work(fixture.Exec("GRANT pgss_visibility_group TO pgss_visibility_b", 0))
		check(visible)
		s.work(fixture.Exec("REVOKE pgss_visibility_group FROM pgss_visibility_b", 0))
		check(denied)
		s.exec("RESET ROLE")
		check(visible)
	})
}
