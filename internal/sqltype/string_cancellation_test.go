package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A real cancellation is triggered deterministically by a context checkpoint,
// rather than a goroutine racing a wall-clock delay against endpoint scanning.
type stringTrimCancellationContext struct {
	context.Context
	cancel          context.CancelFunc
	calls, cancelAt int
}

func (c *stringTrimCancellationContext) Err() error {
	c.calls++
	if c.cancelAt > 0 && c.calls >= c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestStringCoreReviewTrimCancellation(t *testing.T) {
	text := mathTypes()[Text]
	for _, direction := range []string{"btrim", "ltrim", "rtrim"} {
		t.Run(direction, func(t *testing.T) {
			plan, err := BindString(direction, text.Usage(), text.Usage(), []Usage{text.Usage(), text.Usage()}, [2]bool{true, true}, false)
			if err != nil {
				t.Fatal(err)
			}
			// Calibrate only entry/conversion/short-operation checkpoints. A long
			// operation must make additional checkpoints before it can finish.
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &stringTrimCancellationContext{Context: base, cancel: cancel}
			value, err := plan.Evaluate(probe, [2]Value{String(text, "é"), String(text, "é")}, InputEnvironment{}, OutputEnvironment{})
			if got, e := value.Text(); err != nil || e != nil || got != "" {
				t.Fatal("short trim", got, err, e)
			}
			for _, tc := range []struct{ name, source, set string }{
				{"endpoints", strings.Repeat("é", 16384), "é"},
				{"membership", "é", strings.Repeat("🙂", 32768) + "é"},
				{"both", strings.Repeat("é", 4096), strings.Repeat("🙂", 4096) + "é"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, stop := context.WithCancel(context.Background())
					defer stop()
					c := &stringTrimCancellationContext{Context: ctx, cancel: stop, cancelAt: probe.calls + 2}
					_, err := plan.Evaluate(c, [2]Value{String(text, tc.source), String(text, tc.set)}, InputEnvironment{}, OutputEnvironment{})
					if !errors.Is(err, context.Canceled) || c.calls < c.cancelAt {
						t.Fatalf("trim failed to interrupt in-progress work: checkpoints=%d threshold=%d error=%v", c.calls, c.cancelAt, err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("context cancellation became SQL error", err)
					}
				})
			}
		})
	}
}

func TestStringCoreReviewTrimAllocations(t *testing.T) {
	text := mathTypes()[Text]
	prefix := strings.Repeat("é", 4096)
	longSet := strings.Repeat("🙂", 32768) + "é"
	for _, direction := range []string{"btrim", "ltrim", "rtrim"} {
		for _, tc := range []struct {
			name, source, set, both, left, right string
			unary                                bool
		}{
			{"default", " é ", "", "é", "é ", " é", true},
			{"runes", "é🙂X🙂é", "🙂é", "X", "X🙂é", "é🙂X", false},
			{"empty_set", " é ", "", " é ", " é ", " é ", false},
			{"empty_source", "", "é", "", "", "", false},
			{"long_source", prefix + "X" + prefix, "é", "X", "X" + prefix, prefix + "X", false},
			{"long_membership", "éXé", longSet, "X", "Xé", "éX", false},
		} {
			t.Run(direction+"/"+tc.name, func(t *testing.T) {
				args := []Usage{text.Usage(), text.Usage()}
				if tc.unary {
					args = args[:1]
				}
				plan, err := BindString(direction, text.Usage(), text.Usage(), args, [2]bool{true, true}, false)
				if err != nil {
					t.Fatal(err)
				}
				values := [2]Value{String(text, tc.source), String(text, tc.set)}
				want := tc.both
				if direction == "ltrim" {
					want = tc.left
				}
				if direction == "rtrim" {
					want = tc.right
				}
				check := func() {
					got, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
					if err != nil {
						t.Fatal(err)
					}
					if s, err := got.Text(); err != nil || s != want {
						t.Fatal("endpoint/set semantics", s, want, err)
					}
				}
				check()
				if allocations := testing.AllocsPerRun(20, check); allocations != 0 {
					t.Fatalf("immutable trim slice allocates: %g", allocations)
				}
			})
		}
	}
}
