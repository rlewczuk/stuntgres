package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCursorArgumentSource(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		// REL_18_6 read_cursor_args reorders named actual expressions into
		// declaration order and appends quoted formal AS labels. Scalar
		// subqueries and multiple targets require a real executor evaluation.
		s.exec("CREATE FUNCTION pgss_part.pgss_cursor_argument_source() RETURNS integer LANGUAGE plpgsql AS $$DECLARE c CURSOR(a integer,\"Odd 雪\" integer) FOR SELECT a+\"Odd 雪\"; x integer; BEGIN OPEN c(\"Odd 雪\" => (SELECT 22), a => (SELECT /*pgss_arg 雪*/11)); FETCH c INTO x; CLOSE c; RETURN x; END$$")
		s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
		pattern := "%pgss_arg 雪%"
		s.nestedCounts(pattern, false, 0, 0, 0)
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT pgss_part.pgss_cursor_argument_source() AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(33)}})
			if prepared {
				step = fixture.Prepared(step)
			}
			s.work(step)
		}
		s.nestedCounts(pattern, false, 1, 2, 2)
		s.both(fixture.Select("SELECT query,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND NOT toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"query", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "INT8", "INT8"}, [][]any{{"(SELECT /*pgss_arg 雪*/$1) AS a, (SELECT $2) AS \"Odd 雪\"", false, int64(2), int64(2)}}, pattern))
	})
}
