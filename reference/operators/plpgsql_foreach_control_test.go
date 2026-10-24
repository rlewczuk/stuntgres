package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLForeachControl(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, body, result, want string
	}{
		{"snapshot", "a integer[]:='{1,2,3}'; x integer; s text:=''", "FOREACH x IN ARRAY a LOOP s:=s||x::text; a:='{9}'; END LOOP;", "s", "123"},
		{"slice_snapshot", "a integer[]:='{{1,2},{3,4}}'; s text:=''", "FOREACH a SLICE 1 IN ARRAY a LOOP s:=s||a::text; END LOOP;", "s", "{1,2}{3,4}"},
		{"continue_exit", "x integer; n integer:=0", "<<f>> FOREACH x IN ARRAY '{1,2,3,4}'::integer[] LOOP CONTINUE f WHEN x=2; EXIT f WHEN x=4; n:=n+x; END LOOP f;", "n::text||':'||x::text||':'||FOUND::text", "4:4:true"},
		{"outward_transfers", "x integer; y integer; n integer:=0", "<<outer_loop>> FOREACH x IN ARRAY '{1,2,3}'::integer[] LOOP FOREACH y IN ARRAY '{1,2}'::integer[] LOOP CONTINUE outer_loop WHEN x=2; EXIT outer_loop WHEN x=3 AND y=2; n:=n+10*x+y; END LOOP; END LOOP outer_loop;", "n::text||':'||x::text||':'||y::text||':'||FOUND::text", "54:3:2:true"},
		{"found_entry", "x integer; s text:=''", "FOREACH x IN ARRAY '{1,2}'::integer[] LOOP s:=s||FOUND::text||','; END LOOP;", "s||':'||FOUND::text", "false,false,:true"},
		{"empty_retains", "x integer:=99; y integer", "FOREACH y IN ARRAY '{1}'::integer[] LOOP END LOOP; FOREACH x IN ARRAY '{}'::integer[] LOOP END LOOP;", "x::text||':'||FOUND::text", "99:false"},
		{"nested_slices", "x integer; y integer[]; s text:=''", "FOREACH x IN ARRAY '{1,2}'::integer[] LOOP FOREACH y SLICE 1 IN ARRAY '{{3,4},{5,6}}'::integer[] LOOP s:=s||x::text||y::text||';'; END LOOP; END LOOP;", "s", "1{3,4};1{5,6};2{3,4};2{5,6};"},
		{"return_protected", "x integer", "FOREACH x IN ARRAY '{1,2,3}'::integer[] LOOP BEGIN IF x=2 THEN RETURN x::text; END IF; EXCEPTION WHEN OTHERS THEN RETURN 'handler'; END; END LOOP;", "'unreachable'", "2"},
		{"lazy_source", "x integer", "IF false THEN FOREACH x IN ARRAY missing_array LOOP END LOOP; END IF;", "'lazy'", "lazy"},
		{"quoted_boundaries", "x text; s text:=''", `<<"é">> FOREACH x IN ARRAY /* LOOP */ '{"LOOP","END LOOP","SLICE"}'::text[] LOOP s:=s||x||';'; END LOOP "é";`, "s", "LOOP;END LOOP;SLICE;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_control() RETURNS text LANGUAGE plpgsql AS $$DECLARE "+tc.declaration+"; BEGIN "+tc.body+" RETURN "+tc.result+"; END$$")
				plpgsqlValue(t, p, b, c, "SELECT fe_control() AS v", "TEXT", tc.want)
				foreachHeld(t, p, b, c, "SELECT fe_control() AS v", "TEXT", [][]any{nil, nil}, []any{tc.want, tc.want})
			})
		})
	}
	t.Run("source_once_per_entry", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE fe_calls",
				"CREATE FUNCTION fe_source() RETURNS integer[] LANGUAGE SQL AS $$SELECT nextval('fe_calls'); SELECT '{1,2,3}'::integer[]$$",
				"CREATE FUNCTION fe_once() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer; s text:=''; BEGIN FOREACH x IN ARRAY fe_source() LOOP s:=s||x::text; END LOOP; RETURN s; END$$")
			q := "SELECT fe_once() AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			plpgsqlValue(t, p, b, c, "SELECT is_called::text AS v FROM fe_calls", "TEXT", "false")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"123"}}))
			plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM fe_calls", "INT8", int64(1))
			plpgsqlValue(t, p, b, c, q, "TEXT", "123")
			plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM fe_calls", "INT8", int64(3))
		})
	})
	t.Run("statement_atomicity", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE fe_atomic_data(v integer)",
				"CREATE FUNCTION fe_atomic(a text[]) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; RETURN x; END$$")
			plpgsqlError(t, p, c, "INSERT INTO fe_atomic_data VALUES(fe_atomic('{1,2}'::text[])),(fe_atomic('{1,bad}'::text[]))", false,
				plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "fe_atomic(text[])", "FOREACH over array"))
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM fe_atomic_data", "INT8", int64(0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO fe_atomic_data VALUES(fe_atomic('{3,4}'::text[]))", 1))
			plpgsqlValue(t, p, b, c, "SELECT v FROM fe_atomic_data", "INT4", int64(4))
		})
	})
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "protected_transfer", true: "protected_error"}[fail], func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				operation := "EXIT WHEN x=2;"
				want, count := int64(2), int64(2)
				if fail {
					operation = "IF x=2 THEN n:=1/0; END IF;"
					want, count = int64(99), int64(0)
				}
				userOperatorExec(t, p, b, c,
					"CREATE TABLE fe_effects(v integer)",
					"CREATE FUNCTION fe_put(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO fe_effects VALUES(x); SELECT x$$",
					"CREATE FUNCTION fe_protected() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; n integer:=0; BEGIN BEGIN FOREACH x IN ARRAY '{1,2,3}'::integer[] LOOP n:=fe_put(x); "+operation+" END LOOP; EXCEPTION WHEN OTHERS THEN RETURN 99; END; RETURN n; END$$")
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT fe_protected() AS v", []string{"v"}, []string{"INT4"}, [][]any{{want}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM fe_effects", "INT8", count)
					if !fail {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM fe_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM fe_effects", count))
				}
			})
		})
	}
}
