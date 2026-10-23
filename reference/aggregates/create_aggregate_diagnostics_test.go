package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, options, code, message string }{
		{"missing_state", "SFUNC=ca_add", "42P13", "aggregate stype must be specified"},
		{"missing_function", "STYPE=int4", "42P13", "aggregate sfunc must be specified"},
		{"missing_routine", "SFUNC=ca_absent,STYPE=int4", "42883", "function ca_absent(integer, integer) does not exist"},
		{"wrong_return", "SFUNC=ca_wrong,STYPE=int4", "42804", "return type of transition function ca_wrong is not integer"},
		{"runtime_coercion", "SFUNC=ca_wide,STYPE=int4", "42804", "function ca_wide(bigint, bigint) requires run-time type coercion"},
		{"strict_incompatible", "SFUNC=pg_catalog.int84pl,STYPE=int8", "42P13", "must not omit initial value when transition function is strict and transition type is not compatible with input type"},
		{"invalid_seed", "SFUNC=ca_add,STYPE=int4,INITCOND='bad'", "22P02", `invalid input syntax for type integer: "bad"`},
		{"bare_seed", "SFUNC=ca_add,STYPE=int4,INITCOND", "42601", "initcond requires a parameter"},
		{"fraction_space", "SFUNC=ca_add,STYPE=int4,SSPACE=1.5", "42601", "sspace requires an integer value"},
		{"quoted_space", "SFUNC=ca_add,STYPE=int4,SSPACE='32'", "42601", "sspace requires an integer value"},
		{"overflow_space", "SFUNC=ca_add,STYPE=int4,SSPACE=2147483648", "42601", "sspace requires an integer value"},
		{"minimum_space", "SFUNC=ca_add,STYPE=int4,SSPACE=-2147483648", "42601", "sspace requires an integer value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE FUNCTION ca_add(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0),
					fixture.Exec("CREATE FUNCTION ca_wrong(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'x'::text$$", 0),
					fixture.Exec("CREATE FUNCTION ca_wide(bigint,bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", 0))
				q := "CREATE AGGREGATE ca_rejected(integer) (" + tc.options + ")"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ca_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE ca_rejected(integer) (SFUNC=ca_add,STYPE=int4,INITCOND='0')", 0))
				createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_rejected(x) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6)))
			})
		})
	}
	t.Run("duplicate", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateSetup(t, p, b, c)
			q := "CREATE AGGREGATE ca_sum(integer) (SFUNC=ca_add,STYPE=int4)"
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42723", Message: `function "ca_sum" already exists with same argument types`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input", int64(6)))
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE FUNCTION ca_conflict(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("CREATE AGGREGATE ca_conflict(integer) (SFUNC=ca_add,STYPE=int4)", fixture.Diagnostic{Code: "42723", Message: `function "ca_conflict" already exists with same argument types`}))
		})
	})
	t.Run("space_bounds", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE FUNCTION ca_add(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0))
			for _, tc := range []struct {
				name, space string
				want        int64
			}{{"zero", "0", 0}, {"negative", "-1", -1}, {"maximum", "2147483647", 2147483647}} {
				t.Run(tc.name, func(t *testing.T) {
					name := "ca_space_" + tc.name
					createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE "+name+"(integer) (SFUNC=ca_add,STYPE=int4,INITCOND='0',SSPACE="+tc.space+")", 0))
					createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+name+"(x) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6)))
					createAggregateRun(t, p, b, c, fixture.Select("SELECT a.aggtransspace FROM pg_aggregate a JOIN pg_proc p ON p.oid=a.aggfnoid WHERE p.proname='"+name+"'", []string{"aggtransspace"}, []string{"INT4"}, [][]any{{tc.want}}))
				})
			}
		})
	})
}

func TestReferenceCreateAggregateMetadata(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		q := "SELECT p.prokind::text AS kind,p.prorettype=23::oid AS result_type,p.pronargs,p.proisstrict,p.proparallel::text AS parallel,a.aggkind::text AS aggkind,a.aggnumdirectargs,a.aggtransfn=f.oid AS support,a.aggtranstype=23::oid AS state_type,a.aggtransspace,a.agginitval FROM pg_proc p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggtransfn WHERE p.proname='ca_sum' AND f.proname='ca_add'"
		createAggregateBoth(t, p, b, c, fixture.Select(q, []string{"kind", "result_type", "pronargs", "proisstrict", "parallel", "aggkind", "aggnumdirectargs", "support", "state_type", "aggtransspace", "agginitval"}, []string{"TEXT", "BOOL", "INT2", "BOOL", "TEXT", "TEXT", "INT2", "BOOL", "BOOL", "INT4", "TEXT"}, [][]any{{"a", true, int64(1), false, "u", "n", int64(0), true, true, int64(32), "000"}}))
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE AGGREGATE ca_builtin(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4)", 0),
			fixture.Exec("CREATE AGGREGATE ca_text(text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND='')", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.proname,a.aggtransfn=177::oid AS builtin,a.agginitval FROM pg_proc p JOIN pg_aggregate a ON a.aggfnoid=p.oid WHERE p.proname='ca_builtin'", []string{"proname", "builtin", "agginitval"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"ca_builtin", true, nil}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_text(x) AS v FROM(VALUES('a'::text),('b'::text)) s(x) WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{""}}))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input WHERE FALSE", int64(0)))
		createAggregateRun(t, p, b, c,
			fixture.Exec("SET timezone='UTC'", 0),
			fixture.Exec("CREATE FUNCTION ca_hold_clock(timestamptz) RETURNS timestamptz LANGUAGE SQL AS $$SELECT $1$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_clock(*) (SFUNC=ca_hold_clock,STYPE=timestamptz,INITCOND='2000-01-01 00:00:00')", 0),
			fixture.Exec("SET timezone='America/New_York'", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_clock(*)::text AS v FROM ca_input WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{"2000-01-01 00:00:00-05"}}))
		createAggregateRun(t, p, b, c, fixture.Exec("SET timezone='UTC'", 0))
	})
}

func TestReferenceCreateAggregateConsumers(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE VIEW ca_view AS SELECT g,ca_sum(x) AS v FROM ca_input GROUP BY g", 0),
			fixture.Exec("CREATE FUNCTION ca_total() RETURNS integer BEGIN ATOMIC SELECT ca_sum(x) FROM ca_input; END", 0),
			fixture.Exec("CREATE TABLE ca_destination(g integer,v integer)", 0),
			fixture.Exec("INSERT INTO ca_destination SELECT g,ca_sum(x) FROM ca_input GROUP BY g", 2))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,v FROM ca_view ORDER BY g", []string{"g", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,v FROM ca_destination ORDER BY g", []string{"g", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}}))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_total() AS v", int64(6)))
	})
}
