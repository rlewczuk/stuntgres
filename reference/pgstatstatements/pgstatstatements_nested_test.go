package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsNested(t *testing.T) {
	for _, policy := range []string{"top", "all", "none"} {
		t.Run(policy, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				base := "pgss_part.pgss_nested_" + policy
				s.exec("CREATE TABLE " + base + "(k integer)")
				s.run(fixture.Exec("INSERT INTO "+base+" VALUES(7)", 1))
				s.exec("CREATE TABLE " + base + "_audit(k integer)")
				s.exec("CREATE TABLE " + base + "_target(k integer)")
				// Function-level settings prevent SQL-function inlining: the child
				// must actually execute, rather than become part of the outer plan.
				s.exec("CREATE FUNCTION " + base + "_sql() RETURNS integer LANGUAGE sql VOLATILE SET search_path=pg_catalog AS $$SELECT k FROM " + base + "$$")
				s.exec("CREATE FUNCTION " + base + "_pl() RETURNS integer LANGUAGE plpgsql AS $$DECLARE answer integer; BEGIN SELECT k+1 INTO answer FROM " + base + "; RETURN answer; END$$")
				s.exec("CREATE FUNCTION " + base + "_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO " + base + "_audit VALUES(NEW.k); RETURN NEW; END$$")
				s.exec("CREATE TRIGGER pgss_nested_after AFTER INSERT ON " + base + "_target FOR EACH ROW EXECUTE FUNCTION " + base + "_trigger()")
				s.work(fixture.Exec("SET pg_stat_statements.track = '"+policy+"'", 0))
				outerCalls, childCalls := int64(1), int64(0)
				if policy == "all" {
					childCalls = 1
				}
				if policy == "none" {
					outerCalls = 0
				}
				for _, kind := range []string{"sql", "pl"} {
					q := "SELECT " + base + "_" + kind + "() AS pgss_nested_result"
					s.nestedCounts(q, true, 0, 0, 0)
					value := int64(7)
					if kind == "pl" {
						value = 8
					}
					s.work(fixture.Select(q, []string{"pgss_nested_result"}, []string{"INT4"}, [][]any{{value}}))
					s.nestedCounts(q, true, outerCalls, outerCalls, outerCalls)
				}
				// The arithmetic distinguishes the actual SQL and PL child trees;
				// PL/pgSQL may blank its INTO target in the submitted SQL text.
				s.nestedCounts("SELECT k FROM "+base, false, childCalls, childCalls, childCalls)
				s.nestedCounts("SELECT k+%FROM "+base, false, childCalls, childCalls, childCalls)
				q := "INSERT INTO " + base + "_target VALUES(9)"
				s.work(fixture.Exec(q, 1))
				s.nestedCounts("INSERT INTO "+base+"_target VALUES%", true, outerCalls, outerCalls, outerCalls)
				s.nestedCounts("INSERT INTO "+base+"_audit VALUES%", false, childCalls, childCalls, childCalls)
				s.both(fixture.Select("SELECT k FROM "+base+"_audit", []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}}))
			})
		})
	}
	t.Run("effective_owner_and_invocation_restoration", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgss_nested_owner"})
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE ROLE pgss_nested_owner")
			s.exec("GRANT USAGE ON SCHEMA pgss_part TO pgss_nested_owner")
			s.exec("CREATE TABLE pgss_part.pgss_definer_data(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_definer_data VALUES(23)", 1))
			s.exec("GRANT SELECT ON pgss_part.pgss_definer_data TO pgss_nested_owner")
			s.exec("CREATE FUNCTION pgss_part.pgss_definer() RETURNS integer LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog AS $$SELECT k FROM pgss_part.pgss_definer_data$$")
			s.exec("ALTER FUNCTION pgss_part.pgss_definer() OWNER TO pgss_nested_owner")
			s.exec("CREATE FUNCTION pgss_part.pgss_quiet() RETURNS integer LANGUAGE sql VOLATILE SET pg_stat_statements.track='none' AS $$SELECT k AS pgss_quiet_child FROM pgss_part.pgss_definer_data$$")
			s.work(fixture.Exec("SET pg_stat_statements.track = 'all'", 0))
			s.work(fixture.Select("SELECT pgss_part.pgss_definer() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}))
			s.both(fixture.Select("SELECT userid=(SELECT oid FROM pg_roles WHERE rolname='pgss_nested_owner') AS owner,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query='SELECT k FROM pgss_part.pgss_definer_data' AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"owner", "toplevel", "calls", "rows"}, []string{"BOOL", "BOOL", "INT8", "INT8"}, [][]any{{true, false, int64(1), int64(1)}}))
			s.work(fixture.Select("SELECT pgss_part.pgss_quiet() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}))
			s.counts("SELECT % AS pgss_quiet_child%", 0, 0, 0)
			s.work(fixture.Select("SHOW pg_stat_statements.track", []string{"pg_stat_statements.track"}, []string{"TEXT"}, [][]any{{"all"}}))
			s.work(fixture.Select("SELECT k AS pgss_after_quiet FROM pgss_part.pgss_definer_data", []string{"pgss_after_quiet"}, []string{"INT4"}, [][]any{{int64(23)}}))
			s.entry("SELECT k AS pgss_after_quiet FROM pgss_part.pgss_definer_data", 1, 1)
		})
	})
}

func (s *pgssScenario) nestedCounts(pattern string, top bool, groups, calls, rows int64) {
	s.t.Helper()
	q := "SELECT count(*) AS groups,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND toplevel=$2 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	s.both(fixture.Select(q, []string{"groups", "calls", "rows"}, []string{"INT8", "INT8", "INT8"}, [][]any{{groups, calls, rows}}, pattern, top))
}
