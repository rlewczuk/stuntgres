package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckRecipeInitialization(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE DOMAIN domain_s.init_recipe AS integer CONSTRAINT a_false CHECK(FALSE) CONSTRAINT z_error CHECK(1/0>0)", 0))
				run(fixture.Exec("CREATE DOMAIN domain_s.init_descendant AS domain_s.init_recipe", 0))
				run(fixture.Exec("CREATE TABLE domain_s.init_empty(id integer)", 0))
				for _, expression := range []string{
					"COALESCE(1::domain_s.init_recipe,2::domain_s.init_recipe)",
					"CASE WHEN true THEN 1::domain_s.init_recipe ELSE 2::domain_s.init_recipe END",
					"CASE WHEN false THEN 1::domain_s.init_recipe ELSE 2::domain_s.init_recipe END",
					"COALESCE(1::domain_s.init_descendant,2::domain_s.init_descendant)",
					"COALESCE(ARRAY[1]::domain_s.init_recipe[],ARRAY[2]::domain_s.init_recipe[])",
					"CASE WHEN true THEN ARRAY[1]::domain_s.init_recipe[] ELSE ARRAY[2]::domain_s.init_recipe[] END",
				} {
					for _, source := range []string{"public.one WHERE false", "domain_s.init_empty"} {
						q := "SELECT " + expression + " AS v FROM " + source
						t.Run(q, func(t *testing.T) {
							d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
							s := fixture.ErrorDiagnostic(q, d)
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							fixture.RunStep(t, p.Ctx, c, r, s)
						})
					}
				}
				// Dead conditional branches are not executable recipe demand.
				for _, expression := range []string{
					"CASE WHEN false THEN (1::domain_s.init_recipe)::integer ELSE 1 END",
					"COALESCE(1,(1::domain_s.init_recipe)::integer)",
				} {
					s := fixture.Select("SELECT "+expression+" AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
					s.Prepared = prepared
					run(s)
				}
				run(fixture.Exec("CREATE TABLE domain_s.init_default(id integer,v integer DEFAULT COALESCE((1::domain_s.init_recipe)::integer,(2::domain_s.init_recipe)::integer))", 0))
				run(fixture.Exec("INSERT INTO domain_s.init_default VALUES(7,9)", 1))
				for _, q := range []string{
					"INSERT INTO domain_s.init_default(id) SELECT id FROM domain_s.init_empty",
					"INSERT INTO domain_s.init_default(id) SELECT v FROM public.one WHERE false",
				} {
					d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT id,v FROM domain_s.init_default", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
				}
			})
		})
	}
}
