package sqltype

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

// Observe context checkpoints during a real successful evaluation. Transient
// caller PCs distinguish consecutive repeated checkpoints without hard-coded
// source lines, production hooks, sleeps or a concurrent canceling goroutine.
// The PCs are used only to select execution-phase cancellation points; every
// cancellation is then asserted through the real provider's returned outcome.
type mathScalarTraversalContext struct {
	context.Context
	sites []uintptr
	stop  int
}

func (c *mathScalarTraversalContext) Err() error {
	pc, _, _, _ := runtime.Caller(1)
	c.sites = append(c.sites, pc)
	if c.stop > 0 && len(c.sites) >= c.stop {
		return context.Canceled
	}
	return nil
}

func TestMathScalarExpComputationCancellation(t *testing.T) {
	p, inputs := scalarProvider(t, "exp", Numeric, "1.0")
	original := inputs
	traversal := &mathScalarTraversalContext{Context: context.Background()}
	got, err := p.Evaluate(traversal, inputs[0], inputs[1])
	if err != nil || inputs != original {
		t.Fatal("successful checkpoint traversal", got, err)
	}
	text, err := p.ResultUsage().Type.Output(context.Background(), got)
	if err != nil || text != "2.7182818284590452" {
		t.Fatal("independent successful result", text, err)
	}
	// There are two repeated computation phases. For x=1 the independent
	// reduction bound is 1/2^7 <= .01 < 1/2^6, hence exactly seven squarings.
	// Require both phases to be observable so removing an entire loop's
	// checks cannot merely shorten the successful traversal and pass the test.
	type phase struct{ first, count int }
	var repeated []phase
	for first := 0; first < len(traversal.sites); {
		end := first + 1
		for end < len(traversal.sites) && traversal.sites[end] == traversal.sites[first] {
			end++
		}
		if end-first > 1 {
			repeated = append(repeated, phase{first + 1, end - first})
		}
		first = end
	}
	if len(repeated) != 2 || repeated[0].count < 3 || repeated[1].count != 7 {
		t.Fatalf("missing arithmetic-phase checkpoints: repeated=%v total=%d", repeated, len(traversal.sites))
	}
	// Cancel after terms have been accumulated, before first/last squares,
	// after final rounding, and after string export. Checkpoint indices come
	// from the successful traversal, not a guessed fixed preflight count.
	for _, tc := range []struct {
		name       string
		checkpoint int
	}{
		{"Taylor iteration", repeated[0].first + repeated[0].count/2},
		{"first square", repeated[1].first},
		{"last square", repeated[1].first + repeated[1].count - 1},
		{"final rounding", len(traversal.sites) - 1},
		{"final export", len(traversal.sites)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &mathScalarTraversalContext{Context: context.Background(), stop: tc.checkpoint}
			partial, err := p.Evaluate(ctx, inputs[0], inputs[1])
			var diagnostic interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) || partial != (Value{}) || inputs != original {
				t.Fatal("cancellation exposed a result or changed input", tc.checkpoint, partial, err)
			}
			reused, err := p.Evaluate(context.Background(), inputs[0], inputs[1])
			if err != nil || reused != got || inputs != original {
				t.Fatal("provider reuse after cancellation", reused, err)
			}
		})
	}
}
