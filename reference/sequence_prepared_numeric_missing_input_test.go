package reference_test

import (
	"context"
	"testing"
)

func TestReferenceSequencePreparedNumericMissingInput(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			hold := w.hold
			for _, input := range []string{"0", "-", "4294967295"} {
				s := hold("SELECT nextval('" + input + "'::regclass)")
				oid := input
				if oid == "-" {
					oid = "0"
				}
				want := controlsReferenceDiagnostic("XX000", "could not open relation with OID "+oid)
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
				var value int64
				err := s.QueryRowContext(ctx).Scan(&value)
				referenceState(t, err, want.Code)
				referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
			}
		})
	}
}
