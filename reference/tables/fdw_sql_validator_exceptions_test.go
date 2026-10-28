package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwExceptionObjects(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE fdw_exception_log(n integer GENERATED ALWAYS AS IDENTITY,phase text,kind oid,options text)", 0))
	run(fixture.Exec("CREATE TABLE fdw_exception_diagnostics(code text,message text)", 0))
	run(fixture.Exec("CREATE FUNCTION fdw_exception_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fdw_exception_log(phase,kind,options) VALUES('validator',$2,$1::text); SELECT CASE WHEN 'mode=bad'=ANY($1) THEN ($1[1])::integer ELSE 7 END$$", 0))
	run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_exception_validator", 0))
	run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", 0))
	run(fixture.Exec("DELETE FROM fdw_exception_log", 2))
}

func TestReferenceFDWSQLValidatorExceptions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwExceptionObjects(run)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ query, condition, code, message string }{
				{"CREATE FOREIGN TABLE fdw_caught(id integer) SERVER fdw_server OPTIONS(mode 'bad')", "invalid_text_representation", "22P02", `invalid input syntax for type integer: "mode=bad"`},
				{"CREATE FOREIGN TABLE fdw_caught(id integer OPTIONS(remote 'first',remote 'second')) SERVER fdw_server OPTIONS(mode 'good')", "duplicate_object", "42710", `option "remote" provided more than once`},
			} {
				q := "DO $$DECLARE caught_state text; caught_message text; BEGIN INSERT INTO fdw_exception_log(phase) VALUES('prefix'); BEGIN " + tc.query + "; EXCEPTION WHEN " + tc.condition + " THEN GET STACKED DIAGNOSTICS caught_state=RETURNED_SQLSTATE,caught_message=MESSAGE_TEXT; INSERT INTO fdw_exception_diagnostics VALUES(caught_state,caught_message); INSERT INTO fdw_exception_log(phase) VALUES('handled'); END; INSERT INTO fdw_exception_log(phase) VALUES('after'); END$$"
				s := fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT code,message FROM fdw_exception_diagnostics", []string{"code", "message"}, []string{"TEXT", "TEXT"}, [][]any{{tc.code, tc.message}}))
				run(fixture.Select("SELECT phase,kind::bigint AS kind,options FROM fdw_exception_log ORDER BY n", []string{"phase", "kind", "options"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{{"prefix", nil, nil}, {"handled", nil, nil}, {"after", nil, nil}}))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='fdw_caught'", []string{"relname"}, []string{"NAME"}, [][]any{}))
				// Recovery must leave the same connection able to complete another
				// genuine construction, not just suppress the failed nested table.
				valid := fixture.Exec("CREATE FOREIGN TABLE fdw_recovered(id integer OPTIONS(remote 'key')) SERVER fdw_server OPTIONS(mode 'good')", 0)
				valid.Prepared = prepared
				run(valid)
				run(fixture.Select("SELECT relname,relkind AS kind FROM pg_class WHERE relname='fdw_recovered'", []string{"relname", "kind"}, []string{"NAME", "CHAR"}, [][]any{{"fdw_recovered", "f"}}))
				run(fixture.Select("SELECT phase,kind::bigint AS kind,options FROM fdw_exception_log ORDER BY n", []string{"phase", "kind", "options"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{
					{"prefix", nil, nil}, {"handled", nil, nil}, {"after", nil, nil}, {"validator", int64(3118), "{mode=good}"}, {"validator", int64(1249), "{remote=key}"},
				}))
				run(fixture.Exec("DROP FOREIGN TABLE fdw_recovered", 0))
				run(fixture.Exec("DELETE FROM fdw_exception_log", 5))
				run(fixture.Exec("DELETE FROM fdw_exception_diagnostics", 1))
			}
		}
	})
}

func TestReferenceFDWSQLValidatorNestedExceptions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwExceptionObjects(run)
		// A reached utility consumes zero parameters, while the next bound
		// command must still receive the original validator's second formal.
		// SQL text-body declaration checks resolve INSERT against a real table;
		// runtime CREATE replaces that declaration-time, nonpersistent fixture.
		run(fixture.Exec("CREATE TABLE fdw_nested_sql_marker(v integer)", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_exception_sql_utility(text[],oid) RETURNS integer LANGUAGE SQL AS $$CREATE TABLE fdw_nested_sql_marker(v integer); INSERT INTO fdw_nested_sql_marker VALUES($2::integer); SELECT 7$$", 0))
		run(fixture.Exec("DROP TABLE fdw_nested_sql_marker", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_exception_outer_validator(text[],oid) RETURNS integer LANGUAGE plpgsql AS $$DECLARE caught_state text; caught_message text; BEGIN INSERT INTO fdw_exception_log(phase,kind,options) VALUES('outer_prefix',$2,$1::text); IF 'nested=run'=ANY($1) THEN PERFORM fdw_exception_sql_utility($1,$2); BEGIN CREATE FOREIGN TABLE fdw_inner_failed(id integer) SERVER fdw_server OPTIONS(mode 'bad'); EXCEPTION WHEN invalid_text_representation THEN GET STACKED DIAGNOSTICS caught_state=RETURNED_SQLSTATE,caught_message=MESSAGE_TEXT; INSERT INTO fdw_exception_diagnostics VALUES(caught_state,caught_message); INSERT INTO fdw_exception_log(phase,kind,options) VALUES('outer_handled',$2,$1::text); END; END IF; INSERT INTO fdw_exception_log(phase,kind,options) VALUES('outer_after',$2,$1::text); RETURN 7; END$$", 0))
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_native VALIDATOR fdw_exception_outer_validator", 0))
		run(fixture.Exec("CREATE SERVER fdw_outer_server FOREIGN DATA WRAPPER fdw_native", 0))
		run(fixture.Exec("DELETE FROM fdw_exception_log", 4))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("CREATE FOREIGN TABLE fdw_outer_constructed(id integer OPTIONS(remote 'key')) SERVER fdw_outer_server OPTIONS(nested 'run')", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('fdw_inner_failed','fdw_outer_constructed') ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"fdw_outer_constructed"}}))
			run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_outer_constructed'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{nested=run}"}}))
			run(fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_outer_constructed'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{remote=key}"}}))
			run(fixture.Select("SELECT code,message FROM fdw_exception_diagnostics", []string{"code", "message"}, []string{"TEXT", "TEXT"}, [][]any{{"22P02", `invalid input syntax for type integer: "mode=bad"`}}))
			run(fixture.Select("SELECT phase,kind::bigint AS kind,options FROM fdw_exception_log ORDER BY n", []string{"phase", "kind", "options"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{
				{"outer_prefix", int64(3118), "{nested=run}"}, {"outer_handled", int64(3118), "{nested=run}"}, {"outer_after", int64(3118), "{nested=run}"}, {"outer_prefix", int64(1249), "{remote=key}"}, {"outer_after", int64(1249), "{remote=key}"},
			}))
			run(fixture.Select("SELECT v FROM fdw_nested_sql_marker", []string{"v"}, []string{"INT4"}, [][]any{{int64(3118)}}))
			run(fixture.Exec("DROP FOREIGN TABLE fdw_outer_constructed", 0))
			run(fixture.Exec("DROP TABLE fdw_nested_sql_marker", 0))
			run(fixture.Exec("DELETE FROM fdw_exception_log", 5))
			run(fixture.Exec("DELETE FROM fdw_exception_diagnostics", 1))
		}
	})
}
