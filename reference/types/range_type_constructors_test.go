package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeConstructors(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "range_ctor_archive")
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE SCHEMA range_ctor_archive`, 0))
		run(fixture.Exec(`CREATE TYPE public.constructor_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=range_ctor_archive.constructor_spans)`, 0))
		for _, tc := range []struct {
			expression, typ string
			value           any
		}{
			{`public.constructor_span(1,3)`, "CONSTRUCTOR_SPAN", "[1,3)"},
			{`public.constructor_span(1,3,'[]')`, "CONSTRUCTOR_SPAN", "[1,3]"},
			{`public.constructor_span(4,4,'[]')`, "CONSTRUCTOR_SPAN", "[4,4]"},
			{`public.constructor_span(4,4)`, "CONSTRUCTOR_SPAN", "empty"},
			{`public.constructor_span(NULL,3,'[]')`, "CONSTRUCTOR_SPAN", "(,3]"},
			{`public.constructor_span(1,NULL,'[]')`, "CONSTRUCTOR_SPAN", "[1,)"},
			{`public.constructor_span(NULL,NULL)`, "CONSTRUCTOR_SPAN", "(,)"},
			{`public.constructor_spans()`, "CONSTRUCTOR_SPANS", "{}"},
			{`public.constructor_spans(public.constructor_span(1,3,'[]'))`, "CONSTRUCTOR_SPANS", "{[1,3]}"},
			{`public.constructor_spans(public.constructor_span(8,9),public.constructor_span(1,3,'[]'),public.constructor_span(3,4))`, "CONSTRUCTOR_SPANS", "{[1,4),[8,9)}"},
			{`public.constructor_spans(NULL::public.constructor_span)`, "CONSTRUCTOR_SPANS", nil},
			{`public.constructor_spans(VARIADIC ARRAY[public.constructor_span(3,4),public.constructor_span(1,2)])`, "CONSTRUCTOR_SPANS", "{[1,2),[3,4)}"},
			{`public.constructor_spans(VARIADIC ARRAY[]::public.constructor_span[])`, "CONSTRUCTOR_SPANS", "{}"},
			{`public.constructor_spans(VARIADIC NULL::public.constructor_span[])`, "CONSTRUCTOR_SPANS", nil},
		} {
			for _, prepared := range []bool{false, true} {
				s := rangeTypeSelect(b, "SELECT "+tc.expression+" AS r", []string{"r"}, []string{tc.typ}, [][]any{{tc.value}}, nil)
				s.Prepared = prepared
				run(s)
			}
		}
		stmt, err := conn.PrepareContext(p.Ctx, `SELECT public.constructor_span($1::int4,$2::int4,$3::text) AS r`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			lower, upper any
			flags, want  string
		}{{int64(1), int64(3), "[]", "[1,3]"}, {nil, int64(7), "[)", "(,7)"}, {int64(4), int64(4), "()", "empty"}} {
			s := rangeTypeSelect(b, `SELECT public.constructor_span($1::int4,$2::int4,$3::text) AS r`, []string{"r"}, []string{"CONSTRUCTOR_SPAN"}, [][]any{{tc.want}}, nil, tc.lower, tc.upper, tc.flags)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: s.Query}, b.Recorder, s)
		}
		// The companion type is in range_ctor_archive, but PostgreSQL places
		// all five generated routines in the RANGE type's namespace.
		for _, tc := range []struct {
			signature       string
			arity           int64
			strict, isRange bool
		}{
			{"public.constructor_span(integer,integer)", 2, false, true},
			{"public.constructor_span(integer,integer,text)", 3, false, true},
			{"public.constructor_spans()", 0, true, false},
			{"public.constructor_spans(public.constructor_span)", 1, true, false},
			{"public.constructor_spans(public.constructor_span[])", 1, true, false},
		} {
			name, result := "constructor_spans", "range_ctor_archive.constructor_spans"
			if tc.isRange {
				name, result = "constructor_span", "public.constructor_span"
			}
			run(fixture.Select(`SELECT p.proname,p.pronamespace='public'::regnamespace AS in_public,p.proowner=(SELECT typowner FROM pg_type WHERE oid='bool'::regtype) AS bootstrap_owner,p.pronargs::int4,p.proisstrict,p.provolatile::text,p.prorettype=`+rangeLiteral(result)+`::regtype AS result_match FROM pg_proc p WHERE p.oid=`+rangeLiteral(tc.signature)+`::regprocedure::oid`, []string{"proname", "in_public", "bootstrap_owner", "pronargs", "proisstrict", "provolatile", "result_match"}, []string{"NAME", "BOOL", "BOOL", "INT4", "BOOL", "TEXT", "BOOL"}, [][]any{{name, true, true, tc.arity, tc.strict, "i", true}}))
		}
		run(fixture.Select(`SELECT count(DISTINCT oid) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('constructor_span','constructor_spans')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Select(`SELECT count(DISTINCT r) AS n FROM (VALUES ('public.constructor_span(integer,integer)'::regprocedure::oid),('public.constructor_span(integer,integer,text)'::regprocedure::oid),('public.constructor_spans()'::regprocedure::oid),('public.constructor_spans(public.constructor_span)'::regprocedure::oid),('public.constructor_spans(public.constructor_span[])'::regprocedure::oid)) AS signatures(r)`, []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Select(`SELECT count(*) AS n FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('constructor_span','constructor_spans') AND d.refclassid='pg_type'::regclass AND d.deptype='i' AND d.refobjid=p.prorettype`, []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Exec(`DROP TYPE public.constructor_span CASCADE`, 0))
		run(fixture.Select(`SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('constructor_span','constructor_spans') ORDER BY proname`, []string{"proname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceRangeTypeConstructorDiagnostics(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE FUNCTION constructor_failed_multirange() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$`, 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`CREATE TYPE constructor_failed AS RANGE(SUBTYPE=int4)`, fixture.Diagnostic{Code: "42723", Message: `function "constructor_failed_multirange" already exists with same argument types`})
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			rangeTypeAbsent(run, "constructor_failed", "constructor_failed_multirange", "_constructor_failed", "_constructor_failed_multirange")
			run(fixture.Select(`SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='constructor_failed'`, []string{"proname"}, []string{"NAME"}, nil))
			run(fixture.Select(`SELECT constructor_failed_multirange() AS k`, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		run(fixture.Exec(`DROP FUNCTION constructor_failed_multirange()`, 0))
		run(fixture.Exec(`CREATE TYPE constructor_failed AS RANGE(SUBTYPE=int4)`, 0))
		run(fixture.Exec(`CREATE TYPE constructor_span AS RANGE(SUBTYPE=int4)`, 0))
		for _, tc := range []struct {
			name, q, code, message, hint string
			args                         []any
		}{
			{"null_flags", `SELECT constructor_span(1,3,$1::text)`, "22000", "range constructor flags argument must not be null", "", []any{nil}},
			{"reversed_bounds", `SELECT constructor_span($1::int4,$2::int4)`, "22000", "range lower bound must be less than or equal to range upper bound", "", []any{int64(4), int64(1)}},
			{"invalid_flags", `SELECT constructor_span(1,3,$1::text)`, "42601", "invalid range bound flags", `Valid values are "[]", "[)", "(]", and "()".`, []any{"bad"}},
			{"null_first_member", `SELECT constructor_span_multirange($1::constructor_span,$2::constructor_span)`, "22004", "multirange values cannot contain null members", "", []any{nil, "[1,3]"}},
			{"null_last_member", `SELECT constructor_span_multirange($1::constructor_span,$2::constructor_span)`, "22004", "multirange values cannot contain null members", "", []any{"[1,3]", nil}},
			{"null_variadic_array_member", `SELECT constructor_span_multirange(VARIADIC ARRAY[$1::constructor_span,$2::constructor_span])`, "22004", "multirange values cannot contain null members", "", []any{nil, "[1,3]"}},
			{"null_bound_array_member", `SELECT constructor_span_multirange(VARIADIC $1::constructor_span[])`, "22004", "multirange values cannot contain null members", "", []any{`{NULL,"[1,3]"}`}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}, tc.args...)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			})
		}
		for _, query := range []string{
			`SELECT constructor_span_multirange($1::constructor_span) AS m`,
			`SELECT constructor_span_multirange(VARIADIC $1::constructor_span[]) AS m`,
		} {
			for _, prepared := range []bool{false, true} {
				s := rangeTypeSelect(b, query, []string{"m"}, []string{"CONSTRUCTOR_SPAN_MULTIRANGE"}, [][]any{{nil}}, nil, nil)
				s.Prepared = prepared
				run(s)
			}
		}
		for _, tc := range []struct {
			q, signature string
			args         []any
		}{
			{`SELECT constructor_span()`, "constructor_span()", nil},
			{`SELECT constructor_span($1::text,$2::text)`, "constructor_span(text, text)", []any{"1", "3"}},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, tc.args...)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		run(fixture.Exec(rangeTypeClass("constructor_subtract_ops", "int4mi"), 0))
		run(fixture.Exec(`CREATE TYPE constructor_checked AS RANGE(SUBTYPE=int4,SUBTYPE_OPCLASS=constructor_subtract_ops)`, 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`SELECT constructor_checked($1::int4,$2::int4,'[]')`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, int64(-2147483648), int64(2147483647))
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
		}
		run(rangeTypeSelect(b, `SELECT constructor_span(-2147483648,2147483647,'[]') AS r`, []string{"r"}, []string{"CONSTRUCTOR_SPAN"}, [][]any{{"[-2147483648,2147483647]"}}, nil))
		rangeTypeConstructorAdmission(t, p, b)
	})
}
