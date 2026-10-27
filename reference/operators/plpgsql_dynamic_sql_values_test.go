package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDynamicSQLValues(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE ds_values(i integer, s text, flag boolean, wide bigint)",
			"CREATE SEQUENCE ds_eval",
			"CREATE FUNCTION ds_add(a integer,b integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN EXECUTE 'SELECT $2::integer+$1::integer' INTO x USING a,b; RETURN x; END$$",
			"CREATE FUNCTION ds_types(a integer,sa text,f boolean,w bigint) RETURNS TABLE(i integer,s text,flag boolean,wide bigint) LANGUAGE plpgsql AS $$BEGIN RETURN QUERY EXECUTE 'SELECT $1,$2,$3,$4' USING a,sa,f,w; END$$",
			"CREATE FUNCTION ds_mutate(n integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN EXECUTE 'INSERT INTO ds_values VALUES($1,$2,$3,$4)' USING n,'bound',true,5000000000::bigint; EXECUTE 'UPDATE ds_values SET s=$2 WHERE i=$1 RETURNING s' INTO x USING n,'updated'; EXECUTE 'DELETE FROM ds_values WHERE i=$1 RETURNING s' INTO x USING n; RETURN x; END$$",
			"CREATE FUNCTION ds_order() RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN EXECUTE 'SELECT $2::bigint::text||'':''||$1::bigint::text||'':''||$1::bigint::text' INTO x USING nextval('ds_eval'),nextval('ds_eval'); RETURN x; END$$")
		plpgsqlValue(t, p, b, c, "SELECT ds_add(4,7) AS v", "INT4", int64(11))
		plpgsqlValue(t, p, b, c, "SELECT ds_add(9,12) AS v", "INT4", int64(21))
		for _, tc := range []struct {
			query  string
			values [][]any
		}{
			{"SELECT * FROM ds_types(NULL,'typed',true,5000000000)", [][]any{{nil, "typed", true, int64(5000000000)}}},
			{"SELECT * FROM ds_types(4,'wide',false,9223372036854775807)", [][]any{{int64(4), "wide", false, int64(9223372036854775807)}}},
			{"SELECT * FROM ds_types(NULL,NULL,NULL,NULL)", [][]any{{nil, nil, nil, nil}}},
		} {
			sqlExecutionBoth(t, p, b, c, tc.query, []string{"i", "s", "flag", "wide"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, tc.values)
		}
		plpgsqlValue(t, p, b, c, "SELECT ds_mutate(3) AS v", "TEXT", "updated")
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_values", "INT8", int64(0))
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE ds_eval RESTART WITH 1", 0))
			s := fixture.Select("SELECT ds_order() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"2:1:1"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM ds_eval", "INT8", int64(2))
		}
		for _, q := range []string{"DO $$BEGIN EXECUTE 'SELECT 1'; END$$", "DO $$BEGIN EXECUTE 'INSERT INTO ds_values VALUES(8,NULL,NULL,NULL)'; END$$"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
		}
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_values", "INT8", int64(2))
	})
}

func TestReferencePLpgSQLDynamicSQLIdentifiers(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE ds_sentinel(i integer)",
			"CREATE TABLE \"Dyn Mixed\"(\"Value Mixed\" text)",
			"CREATE FUNCTION ds_identifier(v text) RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN EXECUTE format('INSERT INTO %I(%I) VALUES($1)','Dyn Mixed','Value Mixed') USING v; EXECUTE 'SELECT '||quote_ident('Value Mixed')||' FROM '||quote_ident('Dyn Mixed')||' WHERE '||quote_ident('Value Mixed')||'=$1' INTO x USING v; RETURN x; END$$",
			"CREATE FUNCTION ds_data(v text) RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN EXECUTE 'SELECT $1::text' INTO x USING v; RETURN x; END$$",
			"CREATE TABLE ds_names(local_name integer)",
			"CREATE FUNCTION ds_names() RETURNS integer LANGUAGE plpgsql AS $$DECLARE local_name integer:=99; x integer; BEGIN EXECUTE 'SELECT local_name FROM ds_names' INTO x; RETURN x; END$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ds_names VALUES(11)", 1))
		plpgsqlValue(t, p, b, c, "SELECT ds_identifier('x''; DROP TABLE ds_sentinel; --') AS v", "TEXT", "x'; DROP TABLE ds_sentinel; --")
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_sentinel", "INT8", int64(0))
		plpgsqlValue(t, p, b, c, "SELECT ds_data('Dyn Mixed') AS v", "TEXT", "Dyn Mixed")
		plpgsqlValue(t, p, b, c, "SELECT ds_names() AS v", "INT4", int64(11))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION ds_bad_identifier() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN EXECUTE 'SELECT 1 FROM $1' INTO x USING 'ds_sentinel'; RETURN x; END$$")
		d := plpgsqlRuntimeDiagnostic("42601", "syntax error at or near \"$1\"", "ds_bad_identifier()", "EXECUTE")
		d.InternalQuery = "SELECT 1 FROM $1"
		d.InternalPosition = fixture.Position(15)
		plpgsqlError(t, p, c, "SELECT ds_bad_identifier()", true, d)
	})
}
