package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDynamicSQLReturnQuery(t *testing.T) {
	t.Run("append_continue_and_found", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE ds_set_marks(flag boolean)",
				"CREATE FUNCTION ds_set(n integer) RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT i FROM generate_series(1,3) g(i) WHERE i>$1 ORDER BY i' USING n; RETURN QUERY EXECUTE 'SELECT $1::integer' USING 8; INSERT INTO ds_set_marks VALUES(FOUND); RETURN QUERY EXECUTE 'SELECT 9::integer WHERE false'; INSERT INTO ds_set_marks VALUES(FOUND); END$$",
				"CREATE FUNCTION ds_empty() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN RETURN; END$$",
				"CREATE FUNCTION ds_fallthrough() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN END$$",
				"CREATE FUNCTION ds_found() RETURNS TABLE(label text, flag boolean) LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT ''empty''::text,false WHERE false'; RETURN QUERY EXECUTE 'SELECT ''after_empty''::text,$1::boolean' USING FOUND; RETURN QUERY EXECUTE 'SELECT ''after_row''::text,$1::boolean' USING FOUND; END$$")
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT ds_set(0) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(8)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT flag,count(*) AS n FROM ds_set_marks GROUP BY flag ORDER BY flag", []string{"flag", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(1)}, {true, int64(1)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ds_set_marks", 2))
			}
			for _, q := range []string{"SELECT ds_empty() AS v", "SELECT ds_fallthrough() AS v"} {
				s := sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
			s := sqlExecutionStep(b, "SELECT * FROM ds_found()", []string{"label", "flag"}, []string{"TEXT", "BOOL"}, [][]any{{"after_empty", false}, {"after_row", true}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
	})
	t.Run("composite_nested_and_caught_accumulator", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE ds_result AS (id integer, label text)",
				"CREATE FUNCTION ds_composite() RETURNS SETOF ds_result LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT 1::integer,''one''::text UNION ALL SELECT 2::integer,NULL::text'; END$$",
				"CREATE FUNCTION ds_table(empty_result boolean) RETURNS TABLE(id integer,label text) LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT $1::integer,$2::text WHERE NOT $3::boolean' USING 3,NULL::text,empty_result; END$$",
				"CREATE FUNCTION ds_child() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT 4::integer'; END$$",
				"CREATE FUNCTION ds_parent() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT ds_child()'; RETURN QUERY EXECUTE 'SELECT 5::integer'; END$$",
				"CREATE FUNCTION ds_caught() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN BEGIN RETURN QUERY EXECUTE 'SELECT 6::integer'; RAISE EXCEPTION 'caught'; EXCEPTION WHEN raise_exception THEN END; RETURN QUERY EXECUTE 'SELECT 7::integer'; END$$")
			for _, s := range []fixture.Step{
				sqlExecutionStep(b, "SELECT * FROM ds_composite() ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}}),
				sqlExecutionStep(b, "SELECT * FROM ds_table(false)", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}}),
				sqlExecutionStep(b, "SELECT * FROM ds_table(true)", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{}),
				fixture.Select("SELECT ds_parent() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}}),
				fixture.Select("SELECT ds_caught() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}, {int64(7)}}),
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		})
	})
	t.Run("named_composite_typmod", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TYPE ds_short AS (label varchar(3))",
				"CREATE FUNCTION ds_typmod() RETURNS SETOF ds_short LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT ''abc''::varchar(4) WHERE false'; END$$")
			d := plpgsqlRuntimeDiagnostic("42804", "structure of query does not match function result type", "ds_typmod()", "RETURN QUERY")
			d.Detail = "Returned type character varying(4) does not match expected type character varying(3) in column \"label\" (position 1)."
			d.Context = "SQL statement \"SELECT 'abc'::varchar(4) WHERE false\"\n" + d.Context
			plpgsqlError(t, p, c, "SELECT * FROM ds_typmod()", true, d)
		})
	})
	for _, tc := range []struct{ name, command, detail string }{
		{"wrong_type", "SELECT 1::bigint", "Returned type bigint does not match expected type integer in column \"\" (position 1)."},
		{"zero_wrong_type", "SELECT 1::bigint WHERE false", "Returned type bigint does not match expected type integer in column \"\" (position 1)."},
		{"wrong_count", "SELECT 1::integer,2::integer", "Number of returned columns (2) does not match expected column count (1)."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ds_bad_set() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE '"+tc.command+"'; END$$")
				d := plpgsqlRuntimeDiagnostic("42804", "structure of query does not match function result type", "ds_bad_set()", "RETURN QUERY")
				d.Detail = tc.detail
				d.Context = "SQL statement \"" + tc.command + "\"\n" + d.Context
				plpgsqlError(t, p, c, "SELECT ds_bad_set()", true, d)
			})
		})
	}
}
