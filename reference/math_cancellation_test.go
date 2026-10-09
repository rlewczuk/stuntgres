package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceMathCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			result, err := backend.db.ExecContext(ctx, "SELECT round(1.25,1)")
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
