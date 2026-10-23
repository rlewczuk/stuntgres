package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateAtomicity(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE AGGREGATE ca_overflow(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE TABLE ca_error_source(id integer,x integer)", 0),
			fixture.Exec("INSERT INTO ca_error_source VALUES(1,2147483646),(2,1),(3,1)", 3),
			fixture.Exec("CREATE TABLE ca_error_destination(v integer)", 0),
			fixture.Exec("INSERT INTO ca_error_destination VALUES(9)", 1))
		const q = "SELECT ca_overflow(x ORDER BY id) AS v FROM ca_error_source WHERE id<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, 3)
			s.SelectRows = true
			s.ErrorStage = "execute"
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			} else {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, int64(2147483647), 2))
			insert := fixture.ImplicitError("INSERT INTO ca_error_destination SELECT ca_overflow(x ORDER BY id) FROM ca_error_source", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
			insert.Prepared = prepared
			insert.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ca_error_destination", int64(9)))
		}
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE ca_effect_log(v integer)", 0),
			fixture.Exec("CREATE FUNCTION ca_effect_step(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ca_effect_log VALUES($2); SELECT $1+$2; END", 0),
			fixture.Exec("CREATE AGGREGATE ca_effect(integer) (SFUNC=ca_effect_step,STYPE=int4,INITCOND='0')", 0))
		for _, prepared := range []bool{false, true} {
			failing := fixture.ImplicitError("INSERT INTO ca_error_destination SELECT ca_effect(x ORDER BY id) FROM ca_error_source", fixture.Diagnostic{Code: "22003", Message: "integer out of range", Context: "SQL function \"ca_effect_step\" statement 2"})
			failing.Prepared = prepared
			failing.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, failing)
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ca_effect_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ca_error_destination", int64(9)))
		}
	})
}

func TestReferenceCreateAggregateDependencies(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT d.deptype::text AS kind FROM pg_depend d JOIN pg_proc a ON d.objid=a.oid JOIN pg_proc f ON d.refobjid=f.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_proc'::regclass AND a.proname='ca_sum' AND f.proname='ca_add'", []string{"kind"}, []string{"TEXT"}, [][]any{{"n"}}))
		d := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function ca_add(integer,integer) because other objects depend on it", Detail: "function ca_sum(integer) depends on function ca_add(integer,integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("DROP FUNCTION ca_add(integer,integer)", d))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input", int64(6)))
		// CASCADE is intentionally executed through the public route. Its notice is
		// not discarded: the fixture's ordinary recorder retains server notices.
		createAggregateRun(t, p, b, c, fixture.Exec("DROP FUNCTION ca_add(integer,integer) CASCADE", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ca_sum' OR proname='ca_add'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceCreateAggregateSupportSelection(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ca_support_schema")
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE SCHEMA ca_support_schema", 0),
			fixture.Exec("CREATE FUNCTION ca_support_schema.ca_select(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0),
			fixture.Exec("CREATE FUNCTION ca_support_schema.ca_select(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+$2+1000$$", 0),
			fixture.Exec("CREATE FUNCTION ca_support_schema.ca_default_step(integer,integer,integer DEFAULT 1) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2+$3$$", 0),
			fixture.Exec("CREATE FUNCTION ca_support_schema.ca_variadic_step(integer,VARIADIC integer[]) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2[1]$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_default_full(integer,integer) (SFUNC=ca_support_schema.ca_default_step,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE AGGREGATE ca_unexpanded(integer[]) (SFUNC=ca_support_schema.ca_variadic_step,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE AGGREGATE ca_selected(integer) (STYPE=int4,SFUNC=ca_support_schema.ca_select,INITCOND='0')", 0))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_selected(x) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_default_full(x,y) AS v FROM(VALUES(1,10),(2,20)) s(x,y)", int64(33)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_unexpanded(x) AS v FROM(VALUES(ARRAY[3]),(ARRAY[4])) s(x)", int64(7)))
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE AGGREGATE ca_set(integer) (SFUNC=pg_catalog.generate_series,STYPE=int4)", "42804", "function pg_catalog.generate_series(integer, integer) returns a set"},
			{"CREATE AGGREGATE ca_kind(*) (SFUNC=pg_catalog.sum,STYPE=int4,INITCOND='0')", "42883", "function pg_catalog.sum(integer) does not exist"},
			{"CREATE AGGREGATE ca_no_state(integer) (SFUNC=ca_support_schema.ca_select,STYPE=ca_missing_type)", "42704", `type "ca_missing_type" does not exist`},
			{"CREATE AGGREGATE ca_omitted_default(integer) (SFUNC=ca_support_schema.ca_default_step,STYPE=int4,INITCOND='0')", "42883", "function ca_support_schema.ca_default_step(integer, integer) does not exist"},
			{"CREATE AGGREGATE ca_expanded_variadic(integer) (SFUNC=ca_support_schema.ca_variadic_step,STYPE=int4,INITCOND='0')", "42883", "function ca_support_schema.ca_variadic_step(integer, integer) does not exist"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		for _, name := range []string{"ca_omitted_default", "ca_expanded_variadic"} {
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='"+name+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE "+name+"(integer) (SFUNC=ca_support_schema.ca_select,STYPE=int4,INITCOND='0')", 0))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+name+"(x) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6)))
		}
	})
}
