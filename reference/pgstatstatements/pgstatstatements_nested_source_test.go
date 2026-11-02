package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsNestedSource(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE TABLE pgss_part.pgss_source_data(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_source_data VALUES(1),(2)", 2))
		// These are actual SQL statements and a non-simple scalar subquery,
		// not simple PL expressions that bypass executor hooks on PostgreSQL.
		s.exec("CREATE FUNCTION pgss_part.pgss_source_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; n integer; BEGIN PERFORM /*pgss_source_perform 雪*/ 11 FROM pgss_part.pgss_source_data; SELECT /*pgss_source_capture 雪*/ 12 INTO n FROM pgss_part.pgss_source_data ORDER BY k LIMIT 1; FOR r IN SELECT /*pgss_source_loop 雪*/ k,13 AS loop_literal FROM pgss_part.pgss_source_data ORDER BY k LOOP PERFORM /*pgss_source_body 雪*/ 14+r.k; END LOOP; EXECUTE 'SELECT /*pgss_source_dynamic 雪*/ 15' INTO n; RETURN (SELECT /*pgss_source_return 雪*/ 16 FROM pgss_part.pgss_source_data LIMIT 1); END$$")
		s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT pgss_part.pgss_source_run() AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(16)}})
			if prepared {
				step = fixture.Prepared(step)
			}
			s.work(step)
		}
		// PL/pgSQL rewrites PERFORM to SELECT and masks its SQL INTO target
		// in-place. UTF8 comments and all other original bytes remain intact.
		// PostgreSQL resolves r.k as PARAM_EXTERN 5 (FOUND, r, n, the
		// preceding INTO row, then r.k); normalization starts constants at 6.
		// Native instead executes its genuine PLLocalExpr/composite-field
		// binding, with no external Param competing for placeholder numbers.
		bodySource := "SELECT /*pgss_source_body 雪*/ $1+r.k"
		if s.b.PG {
			bodySource = "SELECT /*pgss_source_body 雪*/ $6+r.k"
		}
		want := [][]any{
			{"SELECT /*pgss_source_perform 雪*/ $1 FROM pgss_part.pgss_source_data", false, int64(2), int64(4)},
			{"SELECT /*pgss_source_capture 雪*/ $1" + strings.Repeat(" ", 8) + "FROM pgss_part.pgss_source_data ORDER BY k LIMIT $2", false, int64(2), int64(2)},
			{"SELECT /*pgss_source_loop 雪*/ k,$1 AS loop_literal FROM pgss_part.pgss_source_data ORDER BY k", false, int64(2), int64(4)},
			{bodySource, false, int64(4), int64(4)},
			{"SELECT /*pgss_source_dynamic 雪*/ $1", false, int64(2), int64(2)},
			{"(SELECT /*pgss_source_return 雪*/ $1 FROM pgss_part.pgss_source_data LIMIT $2)", false, int64(2), int64(2)},
		}
		query := "SELECT query,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query LIKE '%pgss_source_%' AND NOT toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
		step := fixture.Select(query, []string{"query", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "INT8", "INT8"}, want)
		step.Bag = true
		s.both(step)
	})
}
