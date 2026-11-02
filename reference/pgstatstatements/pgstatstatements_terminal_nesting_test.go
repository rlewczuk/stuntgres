package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsTerminalNesting(t *testing.T) {
	for _, boundary := range []string{"sql_close", "commit"} {
		for _, track := range []string{"top", "all"} {
			for _, route := range []string{"direct", "prepared"} {
				t.Run(boundary+"/"+track+"/"+route, func(t *testing.T) {
					pgssCase(t, true, func(s *pgssScenario) {
						base := "pgss_part.pgss_nesting_" + boundary + "_" + track + "_" + route
						marker := "pgss_terminal_nesting_" + boundary + "_" + track + "_" + route
						s.exec("CREATE TABLE " + base + "(k integer)")
						s.run(fixture.Exec("INSERT INTO "+base+" VALUES(1),(2)", 2))
						work := func(step fixture.Step) {
							step.Prepared = route == "prepared"
							s.work(step)
						}
						work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
						work(fixture.Exec("SET pg_stat_statements.track_planning=on", 0))
						// Utility tracking is off, but ProcessUtility still nests the
						// genuine DECLARE planner and SQL CLOSE executor disposal.
						work(fixture.Exec("BEGIN", 0))
						defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
						read := "SELECT toplevel,queryid<>0 AS identified,plans,calls,rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY toplevel"
						pattern := "%" + marker + "%"
						check := func(rows [][]any) {
							s.both(fixture.Select(read, []string{"toplevel", "identified", "plans", "calls", "rows"}, []string{"BOOL", "BOOL", "INT8", "INT8", "INT8"}, rows, pattern))
						}
						check(nil)
						work(fixture.Exec("DECLARE pgss_nesting_cursor CURSOR FOR SELECT /*"+marker+"*/ k FROM "+base+" ORDER BY k", 0))
						planned := [][]any{{false, true, int64(1), int64(0), int64(0)}}
						check(planned)
						work(fixture.Select("FETCH FORWARD 1 FROM pgss_nesting_cursor", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
						check(planned)
						work(fixture.Exec("SET pg_stat_statements.track='"+track+"'", 0))
						want := planned
						if boundary == "sql_close" {
							work(fixture.Exec("CLOSE pgss_nesting_cursor", 0))
							if track == "all" {
								want = [][]any{{false, true, int64(1), int64(1), int64(1)}}
							}
							check(want)
							work(fixture.Exec("COMMIT", 0))
						} else {
							// Transaction command-end cleanup happens after the utility
							// hook returns: terminal nesting is zero, not DECLARE's one.
							work(fixture.Exec("COMMIT", 0))
							want = [][]any{{false, true, int64(1), int64(0), int64(0)}, {true, true, int64(0), int64(1), int64(1)}}
						}
						check(want)
						identities := "SELECT count(*) AS groups,count(DISTINCT queryid) AS identities FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
						s.both(fixture.Select(identities, []string{"groups", "identities"}, []string{"INT8", "INT8"}, [][]any{{int64(len(want)), int64(1)}}, pattern))
						// Later successful work cannot duplicate disposal or drain the
						// second tuple of the already closed real executor.
						work(fixture.Select("SELECT 7 AS pgss_nesting_recovered", []string{"pgss_nesting_recovered"}, []string{"INT4"}, [][]any{{int64(7)}}))
						check(want)
					})
				})
			}
		}
	}
}
