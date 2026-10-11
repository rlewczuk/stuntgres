package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedNumericMissingInput(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			hold := w.hold
			for _, input := range []string{"0", "-", "4294967295"} {
				s := hold("SELECT nextval('" + input + "'::regclass)")
				oid := input
				if oid == "-" {
					oid = "0"
				}
				want := fixture.ControlsReferenceDiagnostic("XX000", "could not open relation with OID "+oid)
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				var value int64
				err := s.QueryRowContext(ctx).Scan(&value)
				fixture.State(t, err, want.Code)
				fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
			}
		})
	}
}
