package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A later column option transformation must not preempt an earlier table
// validator. The successful-table variant proves the duplicate is still rejected
// and rolls back the earlier validator's genuine local write.
func TestReferenceFDWSQLValidatorColumnOrder(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE fdw_column_order_log(options text,kind oid)", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_column_order_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_column_order_log VALUES($1::text,$2); SELECT CASE WHEN 'mode=bad'=ANY($1) THEN ($1[1])::integer ELSE 7 END$$", 0))
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_column_order_validator", 0))
		run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", 0))
		run(fixture.Exec("DELETE FROM fdw_column_order_log", 2))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				mode string
				want fixture.Diagnostic
			}{
				{"bad", fdwDiagnostic("22P02", `invalid input syntax for type integer: "mode=bad"`)},
				{"good", fdwDiagnostic("42710", `option "remote" provided more than once`)},
			} {
				d := tc.want
				if tc.mode == "bad" {
					d.Context = `SQL function "fdw_column_order_validator" statement 2`
				}
				q := "CREATE FOREIGN TABLE fdw_column_order(id integer OPTIONS(remote 'first',remote 'second')) SERVER fdw_server OPTIONS(mode '" + tc.mode + "')"
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='fdw_column_order'", []string{"relname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_column_order_log ORDER BY kind,options", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{}))
			}
			// The same validator and column option genuinely succeed when both
			// phases are valid; prior empty logs are rollback, not skipped calls.
			s := fixture.Exec("CREATE FOREIGN TABLE fdw_column_order(id integer OPTIONS(remote 'first')) SERVER fdw_server OPTIONS(mode 'good')", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT options,kind::bigint AS kind FROM fdw_column_order_log ORDER BY kind,options", []string{"options", "kind"}, []string{"TEXT", "INT8"}, [][]any{{"{remote=first}", int64(1249)}, {"{mode=good}", int64(3118)}}))
			run(fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_column_order'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{remote=first}"}}))
			run(fixture.Exec("DROP FOREIGN TABLE fdw_column_order", 0))
			run(fixture.Exec("DELETE FROM fdw_column_order_log", 2))
		}
	})
}
