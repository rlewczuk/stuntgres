package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseRelationsGroupedArgumentErrors(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE grouped_args(idx OID,name TEXT)", 0))
			for _, tc := range []struct {
				query    string
				position string
			}{
				{"SELECT pg_get_viewdef(name) FROM grouped_args GROUP BY idx", "23"},
				{"SELECT idx FROM grouped_args GROUP BY idx ORDER BY pg_get_viewdef(name)", "67"},
			} {
				want := fixture.ControlsReferenceDiagnostic("42803", `column "grouped_args.name" must appear in the GROUP BY clause or be used in an aggregate function`)
				want.Position = tc.position
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.SelectRows = true
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		})
	}
}
