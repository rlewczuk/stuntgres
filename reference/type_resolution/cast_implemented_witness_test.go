package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// One direct first frame, then one held execution of each distinct frame.
// NULL/error/recovery sequences prove frame ownership without route products.
func castWitnessRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, steps ...fixture.Step) {
	t.Helper()
	if len(steps) == 0 {
		t.Fatal("missing witness frames")
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, steps[0])
	stmt, err := b.DB.PrepareContext(p.Ctx, steps[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: steps[0].Query}
	for _, step := range steps {
		if step.Query != steps[0].Query {
			t.Fatal("held witness changed SQL")
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
	}
}

func castWitnessDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	step := fixture.ImplicitError(q, want, args...)
	step.SelectRows = true
	step.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
}
