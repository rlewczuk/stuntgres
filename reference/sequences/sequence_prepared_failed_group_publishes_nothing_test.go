package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedFailedGroupPublishesNothing(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s'),nextval('t')")
			exec("CREATE SEQUENCE a.s START 100")
			exec("ALTER SEQUENCE b.t RENAME TO identity")
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
			want.Position = "29"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			var first, second int64
			err := s.QueryRowContext(ctx).Scan(&first, &second)
			fixture.State(t, err, want.Code)
			fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
			exec("CREATE SEQUENCE a.t START 200")
			w.held(s, fixture.SequenceInt64Step(fixture.Step{SelectRows: true, Columns: []string{"nextval", "nextval"}, Types: []string{"INT8", "INT8"}, Rows: [][]any{{int64(100), int64(200)}}}))
		})
	}
}
