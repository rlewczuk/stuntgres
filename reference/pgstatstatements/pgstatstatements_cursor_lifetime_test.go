package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCursorLifetime(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE TABLE pgss_part.pgss_cursor_data(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_cursor_data VALUES(1),(2)", 2))
		s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
		s.work(fixture.Exec("SET pg_stat_statements.track_planning=on", 0))
		// Utility collection remains disabled: DO must still supply genuine
		// nested SQL executor lifetimes rather than count its wrapper.
		partial := "DO $$DECLARE c CURSOR FOR SELECT /*pgss_cursor_partial*/ k FROM pgss_part.pgss_cursor_data ORDER BY k; x integer; BEGIN OPEN c; FETCH c INTO x; CLOSE c; END$$"
		unused := "DO $$DECLARE c CURSOR FOR SELECT /*pgss_cursor_unused*/ k,7 AS marker FROM pgss_part.pgss_cursor_data ORDER BY k; BEGIN OPEN c; CLOSE c; END$$"
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{partial, unused} {
				step := fixture.Exec(query, 0)
				if prepared {
					step = fixture.Prepared(step)
				}
				s.work(step)
			}
		}
		// A runtime failure is caught by the procedure, but cannot become a
		// successful executor completion when its cursor is later disposed.
		s.work(fixture.Exec("DO $$DECLARE c CURSOR FOR SELECT /*pgss_cursor_failed*/ 10/(k-1) AS quotient FROM pgss_part.pgss_cursor_data ORDER BY k; x integer; BEGIN OPEN c; BEGIN FETCH c INTO x; EXCEPTION WHEN division_by_zero THEN END; CLOSE c; END$$", 0))
		// Conversely an executor closed successfully before its parent's
		// exception remains an actual completed child despite outer rollback.
		failure := fixture.ImplicitError("DO $$DECLARE c CURSOR FOR SELECT /*pgss_cursor_survived*/ k+101 AS value FROM pgss_part.pgss_cursor_data ORDER BY k; x integer; BEGIN OPEN c; FETCH c INTO x; CLOSE c; RAISE EXCEPTION 'pgss cursor parent failed'; END$$", fixture.Diagnostic{Code: "P0001", Message: "pgss cursor parent failed", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"})
		s.work(failure)
		want := [][]any{
			{"SELECT /*pgss_cursor_partial*/ k FROM pgss_part.pgss_cursor_data ORDER BY k", false, int64(2), int64(2), int64(2)},
			{"SELECT /*pgss_cursor_unused*/ k,$1 AS marker FROM pgss_part.pgss_cursor_data ORDER BY k", false, int64(2), int64(2), int64(0)},
			{"SELECT /*pgss_cursor_failed*/ $1/(k-$2) AS quotient FROM pgss_part.pgss_cursor_data ORDER BY k", false, int64(1), int64(0), int64(0)},
			{"SELECT /*pgss_cursor_survived*/ k+$1 AS value FROM pgss_part.pgss_cursor_data ORDER BY k", false, int64(1), int64(1), int64(1)},
		}
		step := fixture.Select("SELECT query,toplevel,plans,calls,rows FROM pgss_part.pg_stat_statements WHERE query LIKE '%pgss_cursor_%' AND NOT toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"query", "toplevel", "plans", "calls", "rows"}, []string{"TEXT", "BOOL", "INT8", "INT8", "INT8"}, want)
		step.Bag = true
		s.both(step)
	})
}
