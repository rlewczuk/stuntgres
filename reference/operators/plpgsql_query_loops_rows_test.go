package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLQueryLoopsOrdered(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE ql_rows(i integer,t text)",
			"CREATE FUNCTION ql_ordered(min_i integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; trace text:=''; BEGIN FOR r IN SELECT i,t FROM ql_rows WHERE i>=min_i ORDER BY i,t LOOP n:=n+1; trace:=trace||r.i||':'||r.t||';'; END LOOP; RETURN n||'|'||trace||'|'||r.i||':'||r.t||'|'||FOUND; END$$",
			"CREATE FUNCTION ql_bag() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; s integer:=0; twos integer:=0; BEGIN FOR r IN SELECT i,t FROM ql_rows LOOP n:=n+1; s:=s+r.i; IF r.i=2 THEN twos:=twos+1; END IF; END LOOP; RETURN n||'|'||s||'|'||twos; END$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ql_rows VALUES(3,'c'),(2,'b'),(1,'a'),(2,'b')", 4))
		q := "SELECT ql_ordered($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			input int
			want  string
		}{{1, "4|1:a;2:b;2:b;3:c;|3:c|true"}, {2, "3|2:b;2:b;3:c;|3:c|true"}, {3, "1|3:c;|3:c|true"}, {1, "4|1:a;2:b;2:b;3:c;|3:c|true"}} {
			s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// No trace assertion: this source deliberately has no ORDER BY.
		plpgsqlValue(t, p, b, c, "SELECT ql_bag() AS v", "TEXT", "4|8|2")
	})
	t.Run("literal_delimiters", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				`CREATE FUNCTION ql_literal_boundary(x integer) RETURNS text LANGUAGE plpgsql AS $fn$DECLARE r record; n integer:=0; BEGIN FOR r IN SELECT /*é中*/ x AS i, 'loop' AS t, $value$loop$value$ AS dollar_text, 'quoted'::text AS "loop" ORDER BY i LOOP n:=n+1; END LOOP; RETURN n||'|'||r.i||':'||r.t||':'||r.dollar_text||':'||r."loop"||'|'||FOUND; END$fn$`)
			q := "SELECT ql_literal_boundary($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input int
				want  string
			}{{7, "1|7:loop:loop:quoted|true"}, {8, "1|8:loop:loop:quoted|true"}, {7, "1|7:loop:loop:quoted|true"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}

func TestReferencePLpgSQLQueryLoopsTargets(t *testing.T) {
	t.Run("ordinal_and_existing_target", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE ql_row(i integer,t text)",
				"CREATE FUNCTION ql_row_target(mode integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r ql_row%ROWTYPE; BEGIN IF mode=1 THEN FOR r IN SELECT 7 AS other_i,'ok'::text AS other_t LOOP END LOOP; ELSIF mode=2 THEN FOR r IN SELECT 8 AS other_i LOOP END LOOP; ELSIF mode=3 THEN FOR r IN SELECT 9,'extra'::text,true LOOP END LOOP; ELSE FOR r IN SELECT '12'::text,'cast'::text LOOP EXIT; END LOOP; END IF; RETURN r.i||':'||coalesce(r.t,'NULL'); END$$",
				"CREATE FUNCTION ql_alias() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; a ALIAS FOR r; BEGIN <<scan>> FOR a IN SELECT 7 AS i LOOP a.i:=a.i+1; EXIT scan; END LOOP scan; RETURN r.i; END$$")
			q := "SELECT ql_row_target($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				mode int
				want string
			}{{1, "7:ok"}, {2, "8:NULL"}, {3, "9:extra"}, {4, "12:cast"}, {1, "7:ok"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.mode)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			plpgsqlValue(t, p, b, c, "SELECT ql_alias() AS v", "INT4", int64(8))
		})
	})
	t.Run("private_shapes", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION ql_shape(mode integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; s text; nested text; BEGIN IF mode=1 THEN FOR r IN SELECT 7 AS a,'outer'::text AS b LOOP nested:=ql_shape(2); s:=r.a||':'||r.b||':'||nested; END LOOP; ELSE FOR r IN SELECT true AS flag LOOP s:=r.flag::text; END LOOP; END IF; FOR r IN SELECT 'tail'::text AS label,9::bigint AS amount LOOP s:=s||':'||r.label||':'||r.amount; END LOOP; FOR r IN SELECT 8 AS a LOOP s:=s||':'||r.a; END LOOP; RETURN s; END$$")
			q := "SELECT ql_shape($1) AS v"
			want := "7:outer:true:tail:9:8:tail:9:8"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				mode int
				want string
			}{{1, want}, {2, "true:tail:9:8"}, {1, want}, {2, "true:tail:9:8"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.mode)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
	t.Run("record_void", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION ql_record_void(x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; total integer:=0; wait_null boolean; null_wait_null boolean; BEGIN FOR r IN SELECT x AS i,pg_sleep(0) AS wait,pg_sleep(NULL::float8) AS null_wait LOOP n:=n+1; total:=total+r.i; END LOOP; wait_null:=r.wait IS NULL; null_wait_null:=r.null_wait IS NULL; RETURN n||'|'||total||'|'||r.i||'|'||wait_null||'|'||coalesce(r.wait::text,'NULL')||'|'||null_wait_null||'|'||FOUND; END$$")
			q := "SELECT ql_record_void($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input int
				want  string
			}{{7, "1|7|7|false||true|true"}, {8, "1|8|8|false||true|true"}, {7, "1|7|7|false||true|true"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
	t.Run("named_extra_void", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE ql_void_row(i integer,t text)",
				"CREATE FUNCTION ql_named_void(x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r ql_void_row%ROWTYPE; n integer:=0; total integer:=0; i_null boolean; t_null boolean; BEGIN FOR r IN SELECT x AS other_i,'kept'::text AS other_t,pg_sleep(0) AS wait,pg_sleep(NULL::float8) AS null_wait LOOP n:=n+1; total:=total+r.i; END LOOP; i_null:=r.i IS NULL; t_null:=r.t IS NULL; RETURN n||'|'||total||'|'||r.i||':'||r.t||'|'||i_null||'|'||t_null||'|'||FOUND; END$$")
			q := "SELECT ql_named_void($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input int
				want  string
			}{{7, "1|7|7:kept|false|false|true"}, {8, "1|8|8:kept|false|false|true"}, {7, "1|7|7:kept|false|false|true"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}

func TestReferencePLpgSQLQueryLoopsEmpty(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE ql_empty_row(i integer,t text)",
			"CREATE FUNCTION ql_empty(take_row boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; BEGIN FOR r IN SELECT 99 AS old_name LOOP END LOOP; FOR r IN SELECT 1 AS a,'x'::text AS b WHERE take_row LOOP n:=n+1; END LOOP; RETURN n||'|'||coalesce(r.a::text,'NULL')||'|'||coalesce(r.b,'NULL')||'|'||FOUND; END$$",
			"CREATE FUNCTION ql_empty_initial() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT 1 AS a,'x'::text AS b WHERE false LOOP RETURN false; END LOOP; RETURN r.a IS NULL AND r.b IS NULL AND NOT FOUND; END$$",
			"CREATE FUNCTION ql_empty_named() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE r ql_empty_row%ROWTYPE; BEGIN r:=ROW(9,'old'); FOR r IN SELECT 1,'x'::text WHERE false LOOP RETURN false; END LOOP; RETURN r.i IS NULL AND r.t IS NULL AND NOT FOUND; END$$",
			"CREATE FUNCTION ql_empty_old() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT 9 AS old_name LOOP END LOOP; FOR r IN SELECT 1 AS a WHERE false LOOP END LOOP; RETURN r.old_name; END$$")
		q := "SELECT ql_empty($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			input bool
			want  string
		}{{true, "1|1|x|true"}, {false, "0|NULL|NULL|false"}, {true, "1|1|x|true"}} {
			s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		plpgsqlValue(t, p, b, c, "SELECT ql_empty_initial() AS v", "BOOL", true)
		plpgsqlValue(t, p, b, c, "SELECT ql_empty_named() AS v", "BOOL", true)
		want := plpgsqlRuntimeDiagnostic("42703", `record "r" has no field "old_name"`, "ql_empty_old()", "RETURN")
		want.Context = "PL/pgSQL expression \"r.old_name\"\n" + want.Context
		plpgsqlError(t, p, c, "SELECT ql_empty_old() AS v", true, want)
		plpgsqlValue(t, p, b, c, "SELECT ql_empty(false) AS v", "TEXT", "0|NULL|NULL|false")
	})
	t.Run("void_descriptor", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION ql_empty_void(x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; i_null boolean; wait_null boolean; BEGIN FOR r IN SELECT 99 AS old_name LOOP END LOOP; FOR r IN SELECT x AS i,pg_sleep(0) AS wait WHERE x<>8 LOOP n:=n+1; END LOOP; i_null:=r.i IS NULL; wait_null:=r.wait IS NULL; RETURN n||'|'||coalesce(r.i::text,'NULL')||'|'||i_null||'|'||wait_null||'|'||coalesce(r.wait::text,'NULL')||'|'||FOUND; END$$",
				"CREATE FUNCTION ql_initial_empty_void() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; i_null boolean; wait_null boolean; BEGIN FOR r IN SELECT 7 AS i,pg_sleep(0) AS wait WHERE false LOOP n:=n+1; END LOOP; i_null:=r.i IS NULL; wait_null:=r.wait IS NULL; RETURN n||'|'||i_null||'|'||wait_null||'|'||FOUND; END$$")
			q := "SELECT ql_empty_void($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input int
				want  string
			}{{7, "1|7|false|false||true"}, {8, "0|NULL|true|true|NULL|false"}, {7, "1|7|false|false||true"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			plpgsqlValue(t, p, b, c, "SELECT ql_initial_empty_void() AS v", "TEXT", "0|true|true|false")
		})
	})
}
