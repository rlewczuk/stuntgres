package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeDiagnostics(t *testing.T) {
	cases := []struct {
		query, state, message, hint, context string
		position                             int
		analysis                             bool
		args                                 []any
	}{
		{"SELECT pg_typeof()", "42883", "function pg_typeof() does not exist", "signature", "", 8, true, nil},
		{"SELECT pg_typeof(1,2)", "42883", "function pg_typeof(integer, integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT format_type()", "42883", "function format_type() does not exist", "signature", "", 8, true, nil},
		{"SELECT format_type(23)", "42883", "function format_type(integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT format_type(23,0,0)", "42883", "function format_type(integer, integer, integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT pg_basetype()", "42883", "function pg_basetype() does not exist", "signature", "", 8, true, nil},
		{"SELECT pg_basetype(23::oid::regtype,23::oid::regtype)", "42883", "function pg_basetype(regtype, regtype) does not exist", "signature", "", 8, true, nil},
		{"SELECT format_type(TRUE,0)", "42883", "function format_type(boolean, integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT format_type(23,TRUE)", "42883", "function format_type(integer, boolean) does not exist", "signature", "", 8, true, nil},
		{"SELECT format_type('integer'::text,0)", "42883", "function format_type(text, integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT pg_basetype(TRUE)", "42883", "function pg_basetype(boolean) does not exist", "signature", "", 8, true, nil},
		{"SELECT pg_basetype('integer'::text)", "42883", "function pg_basetype(text) does not exist", "signature", "", 8, true, nil},
		{"SELECT public.pg_typeof(1)", "42883", "function public.pg_typeof(integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT absent.pg_typeof(1)", "3F000", `schema "absent" does not exist`, "", "", 8, true, nil},
		{"SELECT pg_basetype('td_missing')", "42704", `type "td_missing" does not exist`, "", "", 20, true, nil},
		{"SELECT pg_typeof(NULL::td_missing)", "42704", `type "td_missing" does not exist`, "", "", 24, true, nil},
		{"SELECT /* é */ pg_typeof()", "42883", "function pg_typeof() does not exist", "signature", "", 16, true, nil},
		{"SELECT format_type('4294967296'::oid,NULL)", "22003", `value "4294967296" is out of range for type oid`, "", "", 20, true, nil},
		{"SELECT format_type('not_oid'::oid,NULL)", "22P02", `invalid input syntax for type oid: "not_oid"`, "", "", 20, true, nil},
		{"SELECT pg_basetype('4294967296'::regtype)", "22003", `value "4294967296" is out of range for type oid`, "", "", 20, true, nil},
		{"SELECT format_type($1::oid,NULL)", "22P02", `invalid input syntax for type oid: "not_oid"`, "", "unnamed portal parameter $1 = '...'", 0, false, []any{"not_oid"}},
		{"SELECT format_type($1::oid,NULL)", "22003", `value "4294967296" is out of range for type oid`, "", "unnamed portal parameter $1 = '...'", 0, false, []any{"4294967296"}},
		{"SELECT pg_basetype($1::regtype)", "42704", `type "td_missing" does not exist`, "", "unnamed portal parameter $1 = '...'", 0, false, []any{"td_missing"}},
		{"SELECT format_type(no_such=>23,typemod=>0)", "42883", "function format_type(no_such => integer, typemod => integer) does not exist", "signature", "", 8, true, nil},
		{"SELECT pg_typeof(DISTINCT 1)", "42809", "DISTINCT specified, but pg_typeof is not an aggregate function", "", "", 8, true, nil},
		{"SELECT pg_typeof(1 ORDER BY 1)", "42809", "ORDER BY specified, but pg_typeof is not an aggregate function", "", "", 8, true, nil},
		{"SELECT pg_typeof(1) FILTER (WHERE TRUE)", "42809", "FILTER specified, but pg_typeof is not an aggregate function", "", "", 8, true, nil},
		{"SELECT pg_typeof(1) OVER ()", "42809", "OVER specified, but pg_typeof is not a window function nor an aggregate function", "", "", 8, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.query+"_"+tc.state, func(t *testing.T) {
			typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
				d.Position = fixture.Position(tc.position)
				d.Context = tc.context
				if tc.hint == "signature" {
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				typeDescribeFailure(run, tc.query, d, tc.analysis, tc.args...)
				if !tc.analysis && len(tc.args) > 0 {
					stmt, err := c.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					s := fixture.ErrorDiagnostic(tc.query, d, tc.args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}, b.Recorder, s)
				}
			})
		})
	}
	// Fixed-signature VARIADIC decoration is not automatically an error.
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		typeDescribeValue(run, "pg_typeof(VARIADIC 1)", "REGTYPE", "integer")
	})
	t.Run("has_casts_fixed_variadic", func(t *testing.T) {
		typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn, _ func(fixture.Step)) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			// The builtin-source cast outlives public-schema resets. Register its
			// cleanup before creation, on its still-pinned owner connection.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := conn.ExecContext(ctx, "DROP CAST IF EXISTS (xid AS real)"); err != nil {
					t.Error("fixed VARIADIC cast cleanup", err)
				}
			})
			run := func(s fixture.Step) { fixture.RegtypeRunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE CAST (xid AS real) WITHOUT FUNCTION", 0))
			run(fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"method", "context"}, []string{"TEXT", "TEXT"}, [][]any{{"b", "e"}}))
			for _, tc := range []struct {
				expression, typ string
				resultOID       int64
			}{
				{"pg_catalog.pg_typeof(VARIADIC 1)", "REGTYPE", 2206},
				{"pg_catalog.pg_basetype(VARIADIC 23::oid)", "REGTYPE", 2206},
				{"pg_catalog.format_type(23,VARIADIC NULL::integer)", "TEXT", 25},
			} {
				typeDescribeBoth(run, fixture.Select("SELECT "+tc.expression+" AS v,pg_typeof("+tc.expression+")::oid::bigint AS result_type", []string{"v", "result_type"}, []string{tc.typ, "INT8"}, [][]any{{"integer", tc.resultOID}}))
				if tc.typ == "REGTYPE" {
					typeDescribeValue(run, "("+tc.expression+")::oid::bigint", "INT8", int64(23))
				}
			}
			// HasCasts routes these calls through genuine selected providers;
			// raw/no-op evidence and real IO evidence must survive reentry.
			for _, expression := range []string{"'x'", "NULL", "'x'::anyelement", "NULL::anynonarray", "'x'::text::pg_catalog.unknown", "NULL::text::pg_catalog.unknown"} {
				typeDescribeValue(run, "pg_catalog.pg_typeof("+expression+")::oid::bigint", "INT8", int64(705))
			}
			run(fixture.Exec("CREATE TABLE public.td_selected_strings(id integer,t text)", 0))
			run(fixture.Exec("INSERT INTO public.td_selected_strings VALUES(1,'x'),(2,NULL)", 2))
			typeDescribeBoth(run, fixture.Select("SELECT id,pg_catalog.pg_typeof(t::pg_catalog.unknown)::oid::bigint AS logical,pg_catalog.pg_typeof(t)::oid::bigint AS concrete FROM public.td_selected_strings ORDER BY id", []string{"id", "logical", "concrete"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(705), int64(25)}, {int64(2), int64(705), int64(25)}}))
			run(fixture.Exec("CREATE FUNCTION public.pg_typeof(text) RETURNS regtype LANGUAGE SQL AS $$SELECT 'integer'::regtype$$", 0))
			typeDescribeValue(run, "public.pg_typeof('x'::anyelement)::oid::bigint", "INT8", int64(23))
		})
	})
}
