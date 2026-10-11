package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedParameterMissing(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			exec("CREATE SEQUENCE s START 14")
			s := hold("SELECT nextval($1)")
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			want.Context = `unnamed portal parameter $1 = '...'`
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			var value int64
			err := s.QueryRowContext(ctx, "missing").Scan(&value)
			fixture.State(t, err, want.Code)
			fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
			w.held(s, fixture.SequenceInt64Step(fixture.Step{Args: []any{"s"}, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(14)}}}))
		})
	}
}
