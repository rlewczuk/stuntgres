package reference_test

import (
	"context"
	"testing"
)

func TestReferenceSequencePreparedParameterMissing(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			exec("CREATE SEQUENCE s START 14")
			s := hold("SELECT nextval($1)")
			want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			want.Context = `unnamed portal parameter $1 = '...'`
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			var value int64
			err := s.QueryRowContext(ctx, "missing").Scan(&value)
			referenceState(t, err, want.Code)
			referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
			w.held(s, sequenceInt64Step(referenceStep{args: []any{"s"}, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(14)}}}))
		})
	}
}
