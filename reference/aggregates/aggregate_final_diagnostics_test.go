package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateFinalOptions(t *testing.T) {
	for _, tc := range []struct {
		option string
		extra  bool
	}{
		{"FINALFUNC_EXTRA", true}, {"FINALFUNC_EXTRA=true", true}, {"FINALFUNC_EXTRA=on", true}, {"FINALFUNC_EXTRA=1", true}, {"FINALFUNC_EXTRA='TrUe'", true}, {"FINALFUNC_EXTRA='ON'", true},
		{"FINALFUNC_EXTRA=false", false}, {"FINALFUNC_EXTRA=off", false}, {"FINALFUNC_EXTRA=0", false}, {"FINALFUNC_EXTRA='FaLsE'", false}, {"FINALFUNC_EXTRA='OFF'", false}, {"FINALFUNC_EXTRA=true,FINALFUNC_EXTRA=false", false},
	} {
		t.Run(tc.option, func(t *testing.T) {
			aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE FUNCTION fa_option(integer) RETURNS text LANGUAGE SQL AS $$SELECT 'unary='||$1::text$$", 0),
					fixture.Exec("CREATE FUNCTION fa_option(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'extra='||$1::text||';null='||($2 IS NULL)::text$$", 0),
					fixture.Prepared(fixture.Exec("CREATE AGGREGATE fa_option_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_option,"+tc.option+")", 0)))
				want := "unary=3"
				if tc.extra {
					want = "extra=3;null=true"
				}
				createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_option_sum(x) AS v FROM(VALUES(1),(2)) s(x)", want))
				createAggregateRun(t, p, b, c, fixture.Select("SELECT a.aggfinalextra FROM (SELECT oid FROM pg_proc WHERE proname='fa_option_sum') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"aggfinalextra"}, []string{"BOOL"}, [][]any{{tc.extra}}))
			})
		})
	}
	t.Run("without_final", func(t *testing.T) {
		aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for i, mode := range []string{"read_only", "shareable", "read_write", "read_write,FINALFUNC_MODIFY=read_only"} {
				name := fmt.Sprintf("fa_nofinal_%d", i)
				createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE "+name+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC_EXTRA,FINALFUNC_MODIFY="+mode+")", 0))
				createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+name+"(x) AS v FROM(VALUES(1),(2)) s(x)", int64(3)))
				flag := []string{"r", "s", "w", "r"}[i]
				createAggregateRun(t, p, b, c, fixture.Select("SELECT a.aggfinalfn=0::oid AS nofinal,a.aggfinalextra,a.aggfinalmodify::text AS modify,p.prorettype=23::oid AS result_type FROM (SELECT oid,prorettype FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"nofinal", "aggfinalextra", "modify", "result_type"}, []string{"BOOL", "BOOL", "TEXT", "BOOL"}, [][]any{{true, true, flag, true}}))
			}
		})
	})
}

func aggregateFinalNoResidue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"SELECT count(*) AS n FROM pg_proc WHERE proname='fa_rejected'",
		"SELECT count(*) AS n FROM (SELECT oid FROM pg_proc WHERE proname='fa_rejected') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",
		"SELECT count(*) AS n FROM (SELECT oid FROM pg_proc WHERE proname='fa_rejected') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass",
	} {
		createAggregateRun(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

func TestReferenceAggregateFinalSignatures(t *testing.T) {
	for _, tc := range []struct{ name, signature, options, code, message string }{
		{"bad_mode", "integer", "FINALFUNC=fa_final,FINALFUNC_MODIFY=bad", "42601", `parameter "finalfunc_modify" must be READ_ONLY, SHAREABLE, or READ_WRITE`},
		{"uppercase_mode", "integer", "FINALFUNC=fa_final,FINALFUNC_MODIFY='READ_ONLY'", "42601", `parameter "finalfunc_modify" must be READ_ONLY, SHAREABLE, or READ_WRITE`},
		{"bare_mode", "integer", "FINALFUNC_MODIFY", "42601", "finalfunc_modify requires a parameter"},
		{"extra_number", "integer", "FINALFUNC_EXTRA=2", "42601", "finalfunc_extra requires a Boolean value"},
		{"extra_string", "integer", "FINALFUNC_EXTRA='bad'", "42601", "finalfunc_extra requires a Boolean value"},
		{"missing_final", "integer", "FINALFUNC=fa_absent", "42883", "function fa_absent(integer) does not exist"},
		{"extra_arity", "integer", "FINALFUNC=fa_final,FINALFUNC_EXTRA", "42883", "function fa_final(integer, integer) does not exist"},
		{"wrong_type", "integer", "FINALFUNC=fa_boolean", "42883", "function fa_boolean(integer) does not exist"},
		{"strict_extra", "integer", "FINALFUNC=pg_catalog.int4pl,FINALFUNC_EXTRA", "42P13", "final function with extra arguments must not be declared STRICT"},
		{"strict_zero_extra", "*", "FINALFUNC=pg_catalog.abs,FINALFUNC_EXTRA", "42P13", "final function with extra arguments must not be declared STRICT"},
		{"srf", "integer", "FINALFUNC=pg_catalog.generate_series,FINALFUNC_EXTRA", "42804", "function pg_catalog.generate_series(integer, integer) returns a set"},
		{"runtime_cast", "integer", "FINALFUNC=fa_wide", "42804", "function fa_wide(bigint) requires run-time type coercion"},
		{"omitted_default", "integer", "FINALFUNC=fa_default", "42883", "function fa_default(integer) does not exist"},
		{"variadic_scalar", "integer", "FINALFUNC=fa_variadic", "42883", "function fa_variadic(integer) does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE FUNCTION fa_final(integer) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1::text$$", 0),
					fixture.Exec("CREATE FUNCTION fa_boolean(boolean) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$", 0),
					fixture.Exec("CREATE FUNCTION fa_wide(bigint) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$", 0),
					fixture.Exec("CREATE FUNCTION fa_default(integer,integer DEFAULT 9) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$", 0),
					fixture.Exec("CREATE FUNCTION fa_variadic(VARIADIC integer[]) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$", 0),
					fixture.Exec("CREATE FUNCTION fa_tick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", 0))
				support := "pg_catalog.int4pl"
				if tc.signature == "*" {
					support = "fa_tick"
				}
				q := "CREATE AGGREGATE fa_rejected(" + tc.signature + ") (SFUNC=" + support + ",STYPE=int4,INITCOND='0'," + tc.options + ")"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					aggregateFinalNoResidue(t, p, b, c)
				}
				createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE fa_rejected("+tc.signature+") (SFUNC="+support+",STYPE=int4,INITCOND='0',FINALFUNC=fa_final)", 0))
				arg := "x"
				if tc.signature == "*" {
					arg = "*"
				}
				want := "total=6"
				if tc.signature == "*" {
					want = "total=3"
				}
				createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_rejected("+arg+") AS v FROM(VALUES(1),(2),(3)) s(x)", want))
			})
		})
	}
	t.Run("full_formals", func(t *testing.T) {
		aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE FUNCTION fa_default(integer,integer DEFAULT 9) RETURNS text LANGUAGE SQL AS $$SELECT $1::text||';null='||($2 IS NULL)::text$$", 0),
				fixture.Exec("CREATE AGGREGATE fa_default_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_default,FINALFUNC_EXTRA)", 0),
				fixture.Exec("CREATE FUNCTION fa_array_final(VARIADIC integer[]) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$", 0),
				fixture.Exec("CREATE AGGREGATE fa_array(integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=fa_array_final)", 0))
			createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_default_sum(x) AS v FROM(VALUES(1),(2)) s(x)", "3;null=true"))
			createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_array(x ORDER BY x) AS v FROM(VALUES(2),(1)) s(x)", "{1,2}"))
		})
	})
	t.Run("polymorphic_final", func(t *testing.T) {
		aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE AGGREGATE fa_cardinality(integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=pg_catalog.cardinality)", 0),
				fixture.Exec("CREATE AGGREGATE fa_dimensions(text) (SFUNC=pg_catalog.array_append,STYPE=text[],INITCOND='{}',FINALFUNC=pg_catalog.array_dims)", 0))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT fa_cardinality(x) AS v FROM(VALUES(1),(NULL::integer),(3)) s(x)", int64(3)))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT fa_cardinality(x) AS v FROM(VALUES(1)) s(x) WHERE FALSE", int64(0)))
			createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_dimensions(x) AS v FROM(VALUES('a'::text),('b'::text)) s(x)", "[1:2]"))
		})
	})
}
