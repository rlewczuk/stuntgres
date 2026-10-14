package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSubscriptsBinding(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE DOMAIN subscripts_int AS integer", "CREATE DOMAIN subscripts_bool AS boolean", "CREATE DOMAIN subscripts_array AS integer[]"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, args := range []string{
			"'{1,2}'::integer[],1::smallint", "'{1,2}'::integer[],'1','true'",
			"'{1,2}'::integer[],1::subscripts_int,true::subscripts_bool",
			"'{1,2}'::subscripts_array,1", "'{1,2}'::subscripts_int[],1",
		} {
			seriesBoth(t, p, b, c, fixture.Select("SELECT generate_subscripts("+args+") AS s ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)))
		}
		for _, name := range []string{"pg_catalog.generate_subscripts", `"generate_subscripts"`, "GeNeRaTe_SuBsCrIpTs"} {
			seriesBoth(t, p, b, c, fixture.Select("SELECT "+name+"('{1,2}'::integer[],1) AS s ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)))
		}
		seriesBoth(t, p, b, c, fixture.Select("SELECT generate_subscripts(ALL '{1,2}'::integer[],1) AS s ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)))
		seriesBoth(t, p, b, c, fixture.Select("SELECT generate_subscripts FROM (VALUES(7)) AS v(generate_subscripts)", []string{"generate_subscripts"}, []string{"INT4"}, seriesRows(7)))
	})
}

func TestReferenceSubscriptsDiagnostics(t *testing.T) {
	const noMatch = "No function matches the given name and argument types. You might need to add explicit type casts."
	// Independent signature/parser expectations resolved against pinned PG18.0
	// phase-A observations, including diagnostic fields and failure stages.
	cases := []struct {
		call, token, code, message, hint string
		args                             []any
	}{
		{"generate_subscripts(1,1)", "generate_subscripts", "42883", "function generate_subscripts(integer, integer) does not exist", noMatch, nil},
		{"generate_subscripts('x'::text,1)", "generate_subscripts", "42883", "function generate_subscripts(text, integer) does not exist", noMatch, nil},
		{"generate_subscripts('{1}'::integer[],1::bigint)", "generate_subscripts", "42883", "function generate_subscripts(integer[], bigint) does not exist", noMatch, nil},
		{"generate_subscripts('{1}'::integer[],1::numeric)", "generate_subscripts", "42883", "function generate_subscripts(integer[], numeric) does not exist", noMatch, nil},
		{"generate_subscripts('{1}'::integer[],'1'::text)", "generate_subscripts", "42883", "function generate_subscripts(integer[], text) does not exist", noMatch, nil},
		{"generate_subscripts('{1}'::integer[],1,1)", "generate_subscripts", "42883", "function generate_subscripts(integer[], integer, integer) does not exist", noMatch, nil},
		{"generate_subscripts()", "generate_subscripts", "42883", "function generate_subscripts() does not exist", noMatch, nil},
		{"generate_subscripts('{1}'::integer[])", "generate_subscripts", "42883", "function generate_subscripts(integer[]) does not exist", noMatch, nil},
		{"generate_subscripts('{1}'::integer[],1,true,false)", "generate_subscripts", "42883", "function generate_subscripts(integer[], integer, boolean, boolean) does not exist", noMatch, nil},
		{"generate_subscripts(a => '{1}'::integer[], dim => 1)", "generate_subscripts", "42883", "function generate_subscripts(a => integer[], dim => integer) does not exist", noMatch, nil},
		{"generate_subscripts(VARIADIC ARRAY[1,2])", "generate_subscripts", "42883", "function generate_subscripts(integer[]) does not exist", noMatch, nil},
		{`"GENERATE_SUBSCRIPTS"('{1}'::integer[],1)`, `"GENERATE_SUBSCRIPTS"`, "42883", "function GENERATE_SUBSCRIPTS(integer[], integer) does not exist", noMatch, nil},
		{"public.generate_subscripts('{1}'::integer[],1)", "public.generate_subscripts", "42883", "function public.generate_subscripts(integer[], integer) does not exist", noMatch, nil},
		{"generate_subscripts(NULL,1)", "", "42804", "could not determine polymorphic type because input has type unknown", "", nil},
		{"generate_subscripts('{1,2}',1)", "", "42804", "could not determine polymorphic type because input has type unknown", "", nil},
		{"generate_subscripts($1,1)", "", "42804", "could not determine polymorphic type because input has type unknown", "", []any{"{1}"}},
		{"generate_subscripts('{1}'::integer[],'bad')", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "", nil},
		{"generate_subscripts('{1}'::integer[],1,'bad')", "'bad'", "22P02", `invalid input syntax for type boolean: "bad"`, "", nil},
		{"generate_subscripts('{1}'::integer[],'2147483648'::integer)", "'2147483648'", "22003", `value "2147483648" is out of range for type integer`, "", nil},
		{"generate_subscripts(NULL::integer[],'bad')", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "", nil},
		{"generate_subscripts(NULL::integer[],1,1)", "generate_subscripts", "42883", "function generate_subscripts(integer[], integer, integer) does not exist", noMatch, nil},
		{"generate_subscripts(NULL,'bad')", "", "42804", "could not determine polymorphic type because input has type unknown", "", nil},
		{"generate_subscripts(*)", "generate_subscripts", "42883", "function generate_subscripts() does not exist", noMatch, nil},
		{"generate_subscripts(" + strings.Repeat("1,", 100) + "1)", "generate_subscripts", "54023", "cannot pass more than 100 arguments to a function", "", nil},
		{"$1::text,generate_subscripts($1,1)", "generate_subscripts", "42883", "function generate_subscripts(text, integer) does not exist", noMatch, []any{"{1}"}},
		{"generate_subscripts('{1}'::integer[],'bad',1)", "generate_subscripts", "42883", "function generate_subscripts(integer[], unknown, integer) does not exist", noMatch, nil},
		{"generate_subscripts(NULL,1::bigint)", "generate_subscripts", "42883", "function generate_subscripts(unknown, bigint) does not exist", noMatch, nil},
	}
	for _, dec := range []struct{ call, msg string }{
		{"generate_subscripts(DISTINCT '{1}'::integer[],1)", "DISTINCT specified, but generate_subscripts is not an aggregate function"},
		{"generate_subscripts('{1}'::integer[],1 ORDER BY 1)", "ORDER BY specified, but generate_subscripts is not an aggregate function"},
		{"generate_subscripts('{1}'::integer[],1) FILTER(WHERE true)", "FILTER specified, but generate_subscripts is not an aggregate function"},
		{"generate_subscripts('{1}'::integer[],1) OVER()", "OVER specified, but generate_subscripts is not a window function nor an aggregate function"},
	} {
		cases = append(cases, struct {
			call, token, code, message, hint string
			args                             []any
		}{dec.call, "generate_subscripts", "42809", dec.msg, "", nil})
	}
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.call, func(t *testing.T) {
				for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
					for _, prepared := range []bool{false, true} {
						q := prefix + tc.call
						s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, tc.token, tc.code, tc.message, tc.hint), tc.args...)
						s.Prepared, s.SelectRows = prepared, true
						s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		}
	})
}

func subscriptsParameters(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT generate_subscripts($1::integer[],$2,$3) AS s ORDER BY s"
	st, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
	for _, tc := range []struct {
		a, d, r any
		values  []int64
	}{
		{"[-2:0]={10,NULL,30}", 1, false, []int64{-2, -1, 0}},
		{"[-1:0][3:5]={{1,NULL,3},{4,5,6}}", 2, true, []int64{3, 4, 5}},
		{nil, 1, false, nil}, {"{1}", nil, false, nil}, {"{1}", 1, nil, nil}, {"{1}", 0, false, nil}, {"{}", 1, false, nil},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"s"}, []string{"INT4"}, seriesRows(tc.values...), tc.a, tc.d, tc.r))
	}
	for _, tc := range []struct {
		a, d, r                      any
		parameter, code, msg, detail string
	}{
		{"{1,,2}", 1, false, "1", "22P02", `malformed array literal: "{1,,2}"`, `Unexpected "," character.`},
		{"{1}", "bad", false, "2", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"{1}", 1, "bad", "3", "22P02", `invalid input syntax for type boolean: "bad"`, ""},
		{nil, "bad", false, "2", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"{1}", "2147483648", false, "2", "22003", `value "2147483648" is out of range for type integer`, ""},
	} {
		d := seriesDiagnostic(q, "", tc.code, tc.msg, "")
		d.Detail = tc.detail
		d.Context = "unnamed portal parameter $" + tc.parameter + " = '...'"
		s := fixture.ErrorDiagnostic(q, d, tc.a, tc.d, tc.r)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		seriesBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"s"}, []string{"INT4"}, seriesRows(3, 4, 5), "[-1:0][3:5]={{1,NULL,3},{4,5,6}}", 2, false))
	}
	if !b.PG {
		raw := []byte("[-2:0]={10,NULL,30}")
		rows, err := st.QueryContext(p.Ctx, raw, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for i := range raw {
			raw[i] = 'x'
		}
		subscriptsReadStored(t, rows)
	}
}

func TestReferenceSubscriptsParameters(t *testing.T) {
	subscriptsScenario(t, subscriptsParameters)
}
