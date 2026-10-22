package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineSequenceUsagePopulation(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE SEQUENCE rsu_pop_a", "CREATE SEQUENCE rsu_pop_b", "CREATE TABLE rsu_pop_t(v integer)",
			"CREATE FUNCTION rsu_pop_direct() RETURNS bigint RETURN nextval('public.rsu_pop_a')",
			"CREATE FUNCTION rsu_pop_repeat() RETURNS bigint BEGIN ATOMIC SELECT nextval('public.rsu_pop_a'); SELECT nextval('public.rsu_pop_a')+nextval('public.rsu_pop_b'); END",
			"CREATE PROCEDURE rsu_pop_proc() BEGIN ATOMIC SELECT nextval('public.rsu_pop_a'); END",
			"CREATE FUNCTION rsu_pop_over(integer) RETURNS bigint RETURN nextval('public.rsu_pop_a')",
			"CREATE FUNCTION rsu_pop_over(bigint) RETURNS bigint RETURN nextval('public.rsu_pop_b')",
			"CREATE FUNCTION rsu_pop_regclass() RETURNS regclass RETURN 'public.rsu_pop_a'::regclass",
			"CREATE FUNCTION rsu_pop_quoted() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('public.rsu_pop_a')$$",
			"CREATE FUNCTION rsu_pop_null() RETURNS bigint RETURN NULL::bigint",
			"CREATE FUNCTION rsu_pop_nullreg() RETURNS regclass RETURN NULL::regclass",
			"CREATE FUNCTION rsu_pop_nullinput(regclass) RETURNS bigint RETURN nextval($1)",
			"CREATE FUNCTION rsu_pop_constant() RETURNS text RETURN 'public.rsu_pop_a'",
			"CREATE FUNCTION rsu_pop_table() RETURNS integer BEGIN ATOMIC SELECT v FROM rsu_pop_t; END",
			"CREATE FUNCTION rsu_pop_caller() RETURNS bigint RETURN rsu_pop_direct()",
		} {
			run(fixture.Exec(q, 0))
		}
		var want [][]any
		add := func(name, inputs string, sequences ...string) {
			id := rsuIdentity(t, p, c, "public", name, inputs)
			for _, sequence := range sequences {
				want = append(want, rsuRow(database, "public", name, id, "public", sequence))
			}
		}
		add("rsu_pop_direct", "", "rsu_pop_a")
		add("rsu_pop_repeat", "", "rsu_pop_a", "rsu_pop_b")
		add("rsu_pop_proc", "", "rsu_pop_a")
		add("rsu_pop_over", "integer", "rsu_pop_a")
		add("rsu_pop_over", "bigint", "rsu_pop_b")
		add("rsu_pop_regclass", "", "rsu_pop_a")
		// Both are exactly 63 UTF8 bytes. Specific names must preserve the OID
		// suffix, clipping the routine prefix without splitting the final rune.
		longNames := []string{strings.Repeat("a", 63), strings.Repeat("a", 61) + "é"}
		for _, name := range longNames {
			run(fixture.Exec(`CREATE FUNCTION "`+name+`"() RETURNS bigint RETURN nextval('public.rsu_pop_b')`, 0))
			add(name, "", "rsu_pop_b")
		}
		where := "routine_schema='public' AND (routine_name LIKE 'rsu_pop_%' OR routine_name IN ('" + longNames[0] + "','" + longNames[1] + "'))"
		s := rsuProjection(b.PG, where, want)
		run(s)
		run(fixture.Prepared(s))
		// A NULL input reaches the sequence builtin through a typed formal,
		// rather than requiring unsupported constant-folded call capture.
		run(fixture.Select("SELECT rsu_pop_nullinput(NULL::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{nil}}))
		// Execution of a quoted body must not manufacture a stored hard edge.
		run(fixture.Select("SELECT rsu_pop_quoted() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(s)
	})
}
