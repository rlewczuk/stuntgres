package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			result, err := backend.DB.ExecContext(ctx, "SELECT round(1.25,1)")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
			var diagnostic *stuntgres.Error
			var state interface{ SQLState() string }
			if errors.As(err, &diagnostic) || errors.As(err, &state) {
				t.Fatalf("ordinary cancellation became SQL error: %T %v", err, err)
			}
		})
	}
}
