package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Newly discovered incumbent-provider preservation witnesses. Expectations are
// independently specified; root must observe pinned18.6 and native-red before
// the named-provider competition fix. No backend filter or trial Bind is used.
func TestReferenceRoutineParametersBuiltinLabels(t *testing.T) {
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION public.make_date(year bigint,month bigint,day bigint) RETURNS date LANGUAGE SQL AS $$SELECT DATE '1999-01-01'$$",
			"CREATE FUNCTION public.make_time(hour text,min text,sec text) RETURNS time LANGUAGE SQL AS $$SELECT TIME '00:00:00'$$",
			"CREATE FUNCTION public.make_interval(secs text) RETURNS interval LANGUAGE SQL AS $$SELECT INTERVAL '99 days'$$",
			`CREATE FUNCTION public.array_sort("array" text,descending text) RETURNS integer[] LANGUAGE SQL AS $$SELECT ARRAY[99]$$`,
			"CREATE FUNCTION public.regexp_count(string bool,pattern bool,start bool,flags bool) RETURNS integer LANGUAGE SQL AS $$SELECT 99$$",
			"CREATE FUNCTION public.parse_ident(str boolean,strict text) RETURNS text[] LANGUAGE SQL AS $$SELECT ARRAY['wrong']$$",
			"CREATE FUNCTION public.json_strip_nulls(target text,strip_in_arrays text) RETURNS text LANGUAGE SQL AS $$SELECT 'wrong'$$",
			"CREATE TYPE rp_builtin_row AS (v integer)",
			"CREATE FUNCTION public.json_populate_record(base text,from_json text) RETURNS text LANGUAGE SQL AS $$SELECT 'wrong'$$")
		for _, tc := range []struct {
			q, typ string
			value  any
		}{
			{"SELECT make_date(day=>3,year=>2024,month=>2)::text AS v", "TEXT", "2024-02-03"},
			{"SELECT make_time(sec=>9.5::double precision,hour=>7,min=>8)::text AS v", "TEXT", "07:08:09.5"},
			{"SELECT make_interval(secs=>1.5::double precision)::text AS v", "TEXT", "00:00:01.5"},
			{"SELECT regexp_count(flags=>'i',string=>'Aa',pattern=>'a',start=>1) AS v", "INT4", int64(2)},
			{`SELECT json_strip_nulls(strip_in_arrays=>true,target=>'{"a":[null,1]}'::json)::text AS v`, "TEXT", `{"a":[1]}`},
			{`SELECT (json_populate_record(from_json=>'{"v":7}'::json,base=>NULL::rp_builtin_row)).v AS v`, "INT4", int64(7)},
		} {
			t.Run(tc.q, func(t *testing.T) { userOperatorValue(t, p, b, c, tc.q, tc.typ, tc.value) })
		}
		t.Run("polymorphic reversed slots", func(t *testing.T) {
			s := fixture.ArrayReferenceSelect(`SELECT array_sort(descending=>false,"array"=>ARRAY[3,1]) AS v`, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1,3}"}}, b.PG)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
		t.Run("optional strict reversed slots", func(t *testing.T) {
			s := fixture.ArrayReferenceSelect(`SELECT parse_ident(strict=>true,str=>'"A".b') AS v`, []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{A,b}"}}, b.PG)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
		// An actual SQL winner must still beat a viable label-capable builtin;
		// this is not an unconditional defer-to-provider fallback.
		userOperatorValue(t, p, b, c, "SELECT make_date(day=>3::bigint,year=>2024::bigint,month=>2::bigint)::text AS v", "TEXT", "1999-01-01")
	})
}
