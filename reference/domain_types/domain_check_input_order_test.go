package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckInputOrdering(t *testing.T) {
	for _, tc := range []struct {
		name, query, domain, constraint, code, message, position, context string
		args                                                              []any
		prepareError                                                      bool
	}{
		{name: "parameter_check_before_later_codec", query: "SELECT $1::domain_order.positive AS first,$2::integer AS second FROM public.one", domain: "positive", constraint: "positive_check", code: "23514", args: []any{int64(0), "bad"}, context: "unnamed portal parameter $1 = '...'"},
		{name: "nil_check_before_later_codec", query: "SELECT $1::domain_order.nonnull AS first,$2::integer AS second FROM public.one", domain: "nonnull", constraint: "nonnull_check", code: "23514", args: []any{nil, "bad"}, context: "unnamed portal parameter $1"},
		{name: "earlier_codec_before_parameter_check", query: "SELECT $1::integer AS first,$2::domain_order.positive AS second FROM public.one", code: "22P02", message: `invalid input syntax for type integer: "bad"`, args: []any{"bad", int64(0)}, context: "unnamed portal parameter $1 = '...'"},
		{name: "array_parameter_member_check_before_codec", query: "SELECT $1::domain_order.positive[] AS v FROM public.one", domain: "positive", constraint: "positive_check", code: "23514", args: []any{"{0,bad}"}, context: "unnamed portal parameter $1 = '...'"},
		{name: "composite_parameter_member_check_before_codec", query: "SELECT $1::domain_order.wrapper AS v FROM public.one", domain: "positive", constraint: "positive_check", code: "23514", args: []any{"(0,bad)"}, context: "unnamed portal parameter $1 = '...'"},
		{name: "array_literal_member_check_before_codec", query: "SELECT '{0,bad}'::domain_order.positive[] AS v FROM public.one", domain: "positive", constraint: "positive_check", code: "23514", position: "8", prepareError: true},
		{name: "composite_literal_member_check_before_codec", query: "SELECT '(0,bad)'::domain_order.wrapper AS v FROM public.one", domain: "positive", constraint: "positive_check", code: "23514", position: "8", prepareError: true},
		{name: "array_literal_earlier_codec", query: "SELECT '{bad,0}'::domain_order.positive[] AS v FROM public.one", code: "22P02", message: `invalid input syntax for type integer: "bad"`, position: "8", prepareError: true},
		{name: "composite_literal_earlier_codec", query: "SELECT '(bad,0)'::domain_order.wrapper AS v FROM public.one", code: "22P02", message: `invalid input syntax for type integer: "bad"`, position: "8", prepareError: true},
		{name: "array_parameter_null_member_before_codec", query: "SELECT $1::domain_order.nonnull[] AS v FROM public.one", domain: "nonnull", constraint: "nonnull_check", code: "23514", args: []any{"{NULL,bad}"}, context: "unnamed portal parameter $1 = '...'"},
		{name: "composite_parameter_null_member_before_codec", query: "SELECT $1::domain_order.required_wrapper AS v FROM public.one", domain: "nonnull", constraint: "nonnull_check", code: "23514", args: []any{"(,bad)"}, context: "unnamed portal parameter $1 = '...'"},
		{name: "array_literal_null_member_before_codec", query: "SELECT '{NULL,bad}'::domain_order.nonnull[] AS v FROM public.one", domain: "nonnull", constraint: "nonnull_check", code: "23514", position: "8", prepareError: true},
		{name: "outer_array_physical_input_before_check", query: "SELECT '{0,bad}'::domain_order.outer_array AS v FROM public.one", code: "22P02", message: `invalid input syntax for type integer: "bad"`, position: "8", prepareError: true},
		{name: "outer_array_check_during_execute", query: "SELECT '{0}'::domain_order.outer_array AS v FROM public.one", domain: "outer_array", constraint: "outer_array_check", code: "23514"},
		{name: "outer_composite_member_before_outer_check", query: "SELECT '(0,bad)'::domain_order.outer_wrapper AS v FROM public.one", domain: "positive", constraint: "positive_check", code: "23514", position: "8", prepareError: true},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "domain_order")
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						conn, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
						for _, q := range []string{
							"CREATE SCHEMA domain_order",
							"CREATE DOMAIN domain_order.positive AS integer CONSTRAINT positive_check CHECK (VALUE>0)",
							"CREATE DOMAIN domain_order.nonnull AS integer CONSTRAINT nonnull_check CHECK (VALUE IS NOT NULL)",
							"CREATE TYPE domain_order.wrapper AS (first domain_order.positive,second integer)",
							"CREATE TYPE domain_order.required_wrapper AS (first domain_order.nonnull,second integer)",
							"CREATE DOMAIN domain_order.outer_array AS integer[] CHECK (FALSE)",
							"CREATE DOMAIN domain_order.outer_wrapper AS domain_order.wrapper CHECK (FALSE)",
							"CREATE TABLE public.one (v integer)",
						} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO public.one VALUES (1)", 1))
						message := tc.message
						if tc.code == "23514" {
							message = "value for domain domain_order." + tc.domain + " violates check constraint \"" + tc.constraint + "\""
						}
						want := fixture.ControlsReferenceDiagnostic(tc.code, message)
						want.Position, want.Context = tc.position, tc.context
						if tc.domain != "" {
							want.Schema, want.DataType, want.Constraint = "domain_order", tc.domain, tc.constraint
						}
						s := fixture.ErrorDiagnostic(tc.query, want, tc.args...)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared && tc.prepareError {
							s.ErrorStage = "prepare"
						}
						run(s)
					})
				}
			})
		}
	}
}
