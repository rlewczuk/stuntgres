package reference_test

import (
	"context"
	"testing"
)

func TestReferenceSequencePreparedFailedGroupPublishesNothing(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s'),nextval('t')")
			exec("CREATE SEQUENCE a.s START 100")
			exec("ALTER SEQUENCE b.t RENAME TO identity")
			want := controlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
			want.Position = "29"
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			var first, second int64
			err := s.QueryRowContext(ctx).Scan(&first, &second)
			referenceState(t, err, want.Code)
			referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
			exec("CREATE SEQUENCE a.t START 200")
			w.held(s, sequenceInt64Step(referenceStep{selectRows: true, columns: []string{"nextval", "nextval"}, types: []string{"INT8", "INT8"}, rows: [][]any{{int64(100), int64(200)}}}))
		})
	}
}
