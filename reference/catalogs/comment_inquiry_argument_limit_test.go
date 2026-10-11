package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryArgumentLimit(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, rec := backend.DB, backend.Recorder
					run := func(s fixture.Step) { t.Helper(); s.NonContext = nonContext; fixture.RunStep(t, p.Ctx, db, rec, s) }
					run(fixture.Exec("CREATE TABLE inquiry_limits(id INT)", 0))
					const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					nulls100 := strings.TrimSuffix(strings.Repeat("NULL,", 100), ",")
					unknowns100 := strings.TrimSuffix(strings.Repeat("unknown, ", 100), ", ")
					// Independent PG18 expectations, not records copied from native:
					// transform every child, apply FUNC_MAX_ARGS=100, then resolve
					// the selected fixed signature. Root observes the pinned oracle.
					for _, fn := range []struct {
						name, signature, lastPosition string
					}{
						{"col_description", "function col_description(" + unknowns100 + ") does not exist", "524"},
						{"obj_description", "function obj_description(" + unknowns100 + ") does not exist", "524"},
						{"shobj_description", "function shobj_description(" + unknowns100 + ") does not exist", "526"},
					} {
						for _, tc := range []struct {
							label, query string
							want         fixture.Diagnostic
						}{
							{"101-null", "SELECT " + fn.name + "(" + nulls100 + ",NULL)", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
							{"100-null", "SELECT " + fn.name + "(" + nulls100 + ")", fixture.Diagnostic{Code: "42883", Message: fn.signature, Position: "8", Hint: hint}},
							{"last-missing-column", "SELECT " + fn.name + "(" + nulls100 + ",missing) FROM inquiry_limits", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fn.lastPosition}},
						} {
							t.Run(fn.name+"/"+tc.label+"/Direct", func(t *testing.T) {
								s := fixture.ImplicitError(tc.query, tc.want)
								s.SelectRows = true
								s.ErrorStage = "execute"
								s.NonContext = nonContext
								fixture.RunStep(t, p.Ctx, db, rec, s)
							})
							t.Run(fn.name+"/"+tc.label+"/Prepare", func(t *testing.T) {
								s := fixture.ImplicitError(tc.query, tc.want)
								s.SelectRows = true
								s.ErrorStage = "prepare"
								s.NonContext = nonContext
								fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
							})
						}
					}
				})
			}
		})
	}
}
