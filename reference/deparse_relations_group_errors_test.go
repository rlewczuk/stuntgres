package reference_test

import "testing"

func TestReferenceDeparseRelationsGroupedArgumentErrors(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE grouped_args(idx OID,name TEXT)", 0))
			for _, tc := range []struct {
				query    string
				position string
			}{
				{"SELECT pg_get_viewdef(name) FROM grouped_args GROUP BY idx", "23"},
				{"SELECT idx FROM grouped_args GROUP BY idx ORDER BY pg_get_viewdef(name)", "67"},
			} {
				want := controlsReferenceDiagnostic("42803", `column "grouped_args.name" must appear in the GROUP BY clause or be used in an aggregate function`)
				want.Position = tc.position
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic(tc.query, want)
					s.selectRows = true
					s.prepared = prepared
					if prepared {
						s.errorStage = "prepare"
					} else {
						s.errorStage = "execute"
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
			}
		})
	}
}
