package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckDemandWrappers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE DOMAIN domain_s.wrapper_positive AS integer CONSTRAINT wrapper_positive_check CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.wrapper_required AS integer CONSTRAINT wrapper_required_check CHECK(VALUE IS NOT NULL)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.wrapper_empty(id integer)", 0))
				for _, tc := range []struct{ expression, typ string }{
					{"abs((0::domain_s.wrapper_positive)::integer)", "INT4"},
					{"COALESCE(0::domain_s.wrapper_positive,1::domain_s.wrapper_positive)", "INT4"},
					{"COALESCE(NULL::integer,0::domain_s.wrapper_positive)", "INT4"},
					{"COALESCE(NULL::domain_s.wrapper_required,1::domain_s.wrapper_required)", "INT4"},
					{"CASE WHEN true THEN 0::domain_s.wrapper_positive ELSE 1::domain_s.wrapper_positive END", "INT4"},
					{"0::domain_s.wrapper_positive=ANY(ARRAY[1,2])", "BOOL"},
				} {
					for _, source := range []string{"public.one WHERE false", "public.one WHERE v<0", "domain_s.wrapper_empty"} {
						q := "SELECT " + tc.expression + " AS result FROM " + source
						t.Run(q, func(t *testing.T) {
							s := fixture.Select(q, []string{"result"}, []string{tc.typ}, nil)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, r, s)
						})
					}
				}
				for _, expression := range []string{
					"abs((0::domain_s.wrapper_positive)::integer)",
					"COALESCE(0::domain_s.wrapper_positive,1::domain_s.wrapper_positive)",
				} {
					run(domainCheckFailure("SELECT "+expression+" AS result FROM public.one", "wrapper_positive", "wrapper_positive_check", true, prepared))
				}
				for _, expression := range []string{
					"abs((1::domain_s.wrapper_positive)::integer)",
					"COALESCE(1::domain_s.wrapper_positive,NULL::domain_s.wrapper_positive)",
					"COALESCE((SELECT NULL::domain_s.wrapper_required FROM public.one WHERE false),1::domain_s.wrapper_required)",
				} {
					s := fixture.Select("SELECT "+expression+" AS result FROM public.one", []string{"result"}, []string{"INT4"}, [][]any{{int64(1)}})
					s.Prepared = prepared
					run(s)
				}
			})
		})
	}
}
