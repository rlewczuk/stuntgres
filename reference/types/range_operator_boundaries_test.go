package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeOperatorBoundaries(t *testing.T) {
	for _, f := range operatorFamilies[3:] {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				q := "CREATE TABLE temporal_operator(v " + f.multi() + ")"
				if b.PG {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
					finite := "{" + f.span(f.one, f.three) + "}"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO temporal_operator VALUES ("+rangeLiteral(finite)+"),('{}'),(NULL)", 3))
					// Real upstream signatures succeed; this is not a missing-operator claim.
					operatorBool(t, p, b, "v @> v", "temporal_operator WHERE v IS NOT NULL", [][]any{{true}, {true}})
					operatorBool(t, p, b, "v && v", "temporal_operator WHERE NOT isempty(v)", [][]any{{true}})
					operatorBool(t, p, b, "v && v", "temporal_operator WHERE isempty(v)", [][]any{{false}})
				} else {
					d := fixture.ControlsReferenceDiagnostic("0A000", "unsupported column type")
					d.Position = timestampRangeComparisonPosition(q, strings.Index(q, f.multi()))
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
					q = "CREATE DOMAIN temporal_span AS " + f.multi()
					d = fixture.ControlsReferenceDiagnostic("42804", "\""+f.multi()+"\" is not a valid base type for a domain")
					d.Position = timestampRangeComparisonPosition(q, strings.Index(q, f.multi()))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, d))
				}
			})
		})
	}
}
