package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineHostObjects(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hostFunction(t, p, b, c, "host_inc", "increment-v1", hostIncrementPython, "", true)
		hostFunction(t, p, b, c, "host_double", "double-v1", hostDoublePython, "", true)
		hostExec(t, p, b, c, "CREATE TABLE host_input(v hstore)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_input VALUES ('a=>1,b=>NULL')", 1))
		want := [][]any{{"a", "2"}, {"b", nil}, {"c", "3"}}
		hostBag(t, p, b, c, "host_inc('a=>1,b=>NULL'::hstore)", want)
		hostBag(t, p, b, c, "host_inc((SELECT v FROM host_input))", want)
		q := "SELECT key,value FROM each(host_inc($1::hstore)) ORDER BY key"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, input := range []struct {
			value string
			a     string
		}{{"a=>1,b=>NULL", "2"}, {"a=>7,b=>NULL", "8"}, {"a=>1,b=>NULL", "2"}} {
			s := fixture.Select(q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", input.a}, {"b", nil}, {"c", "3"}}, input.value)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		hostBag(t, p, b, c, "host_double('a=>7,b=>NULL'::hstore)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
		hostBag(t, p, b, c, "(SELECT v FROM host_input)", [][]any{{"a", "1"}, {"b", nil}})
		hostFunction(t, p, b, c, "host_copy", "copy-v1", "assert isinstance(v, dict)\nreturn dict(v)", "", true)
		hostBag(t, p, b, c, "host_copy(''::hstore)", [][]any{})
		hostBag(t, p, b, c, `host_copy(hstore(ARRAY['é','quote"','slash\\'],ARRAY['雪','x"y','a\\b']))`, [][]any{{"quote\"", "x\"y"}, {`slash\\`, `a\\b`}, {"é", "雪"}})
		for _, q := range []string{"SELECT host_inc('a=>1'::hstore) AS v", "SELECT host_inc(v) AS v FROM host_input WHERE false", "SELECT host_copy(''::hstore) AS v", "SELECT host_inc(NULL::hstore) AS v WHERE false"} {
			hostMetadata(t, p, b, c, q)
		}
		// Actual released pair, current SQL type and authentic internal signatures.
		hostRows(t, p, b, c, "SELECT t.trftype=h.oid AS type_ok,t.trflang=l.oid AS language_ok,f.proargtypes='2281'::oidvector AS from_arg,f.prorettype=2281 AS from_result,r.proargtypes='2281'::oidvector AS to_arg,r.prorettype=h.oid AS to_result FROM pg_transform t JOIN pg_type h ON h.oid=t.trftype JOIN pg_language l ON l.oid=t.trflang JOIN pg_proc f ON f.oid=t.trffromsql JOIN pg_proc r ON r.oid=t.trftosql WHERE h.typname='hstore' AND l.lanname='"+hostRuntimeLanguage(b)+"'", []string{"type_ok", "language_ok", "from_arg", "from_result", "to_arg", "to_result"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}})
		hostRows(t, p, b, c, "SELECT pg_typeof(host_inc('a=>1'::hstore))='hstore'::regtype AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
	})
}

func TestReferenceRoutineHostNulls(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body := "if v is None:\n    return {'n': 'handled'}\nassert isinstance(v, dict)\nreturn v"
		hostFunction(t, p, b, c, "host_null", "null-v1", body, "CALLED ON NULL INPUT", true)
		hostFunction(t, p, b, c, "host_strict", "null-v1", body, "STRICT", true)
		for _, fn := range []string{"host_null", "host_strict"} {
			hostBag(t, p, b, c, fn+"(''::hstore)", [][]any{})
			hostBag(t, p, b, c, fn+`('b=>NULL,s=>"NULL"'::hstore)`, [][]any{{"b", nil}, {"s", "NULL"}})
			q := "SELECT " + fn + "($1::hstore) IS NULL AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, input := range []any{nil, "", "b=>NULL", nil} {
				want := input == nil && fn == "host_strict"
				hostRows(t, p, b, held, q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, input)
			}
			stmt.Close()
		}
		hostBag(t, p, b, c, "host_null(NULL::hstore)", [][]any{{"n", "handled"}})
		hostRows(t, p, b, c, "SELECT host_strict(NULL::hstore) IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
		hostMetadata(t, p, b, c, "SELECT host_strict(NULL::hstore) AS v")
		// STRICT bypass must suppress a real failing program, not only a counter.
		hostFunction(t, p, b, c, "host_strict_fail", "fail-v1", "raise ValueError('strict program reached')", "STRICT", true)
		hostRows(t, p, b, c, "SELECT host_strict_fail(NULL::hstore) IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
	})
}

func TestReferenceRoutineHostDefaults(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// No TRANSFORM opt-in: the real language default is str, not dict.
		hostFunction(t, p, b, c, "host_default", "text-default-v1", "assert isinstance(v, str)\nreturn v", "", false)
		hostBag(t, p, b, c, "host_default('a=>7,b=>NULL'::hstore)", [][]any{{"a", "7"}, {"b", nil}})
		hostFunction(t, p, b, c, "host_explicit", "increment-v1", hostIncrementPython, "", true)
		hostBag(t, p, b, c, "host_explicit('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		if !b.PG {
			hostNativeDirections(t, p, b, c)
			return
		}
		// Genuine one-direction pairs on distinct authentic handler aliases. The
		// absent direction uses PL/Python's real default, not a fabricated cast.
		for _, direction := range []string{"from", "to", "none"} {
			lang := "host_" + direction
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS "+lang+" CASCADE")()
			hostExec(t, p, b, c, "CREATE LANGUAGE "+lang+" HANDLER plpython3_call_handler")
			body := "assert isinstance(v, str)\nreturn v"
			if direction == "from" {
				hostExec(t, p, b, c, "CREATE TRANSFORM FOR hstore LANGUAGE "+lang+" (FROM SQL WITH FUNCTION hstore_to_plpython3(internal))")
				body = "assert isinstance(v, dict)\nreturn '\"a\"=>\"9\",\"b\"=>NULL'"
			} else if direction == "to" {
				hostExec(t, p, b, c, "CREATE TRANSFORM FOR hstore LANGUAGE "+lang+" (TO SQL WITH FUNCTION plpython3_to_hstore(internal))")
				body = "assert isinstance(v, str)\nreturn {'a':'9', 'b':None}"
			}
			q := "CREATE FUNCTION host_direction_" + direction + "(v hstore) RETURNS hstore LANGUAGE " + lang + " TRANSFORM FOR TYPE hstore AS '" + hostQuote(body) + "'"
			if direction == "none" {
				// The pinned oracle requires an actual pair for explicit opt-in;
				// omitting opt-in instead selects the real language default.
				hostError(t, p, c, q, fixture.ControlsReferenceDiagnostic("42704", `transform for type hstore language "host_none" does not exist`))
				hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_direction_none'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
				q = "CREATE FUNCTION host_direction_none(v hstore) RETURNS hstore LANGUAGE host_none AS '" + hostQuote(body) + "'"
			}
			hostExec(t, p, b, c, q)
			want := "9"
			if direction == "none" {
				want = "7"
			}
			hostBag(t, p, b, c, "host_direction_"+direction+"('a=>7,b=>NULL'::hstore)", [][]any{{"a", want}, {"b", nil}})
			count := int64(1)
			if direction == "none" {
				count = 0
			}
			hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname=$1", []string{"v"}, []string{"INT8"}, [][]any{{count}}, lang)
		}
		hostError(t, p, c, "CREATE FUNCTION host_unavailable(v hstore) RETURNS hstore LANGUAGE absent_host AS 'increment-v1'", fixture.ControlsReferenceDiagnostic("42704", `language "absent_host" does not exist`))
	})
}
