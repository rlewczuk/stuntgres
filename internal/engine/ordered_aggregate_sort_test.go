package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// sortCancelContext cancels once the ordered-aggregate sort comparator is on
// the stack, so cancellation is observed inside the sort, not before it.
type sortCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	entered bool
}

func (c *sortCancelContext) Err() error {
	if !c.entered {
		var pcs [32]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".sortOrderedAggregate.func1") {
				c.entered = true
				c.cancel()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestSortOrderedAggregateCancellation(t *testing.T) {
	var float8 *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Float8 {
			float8 = d
		}
	}
	comparison, err := sqltype.BindOrdering(float8.Usage())
	if err != nil {
		t.Fatal(err)
	}
	tuples := make([]sqltype.Value, 0, 8)
	for _, n := range []float64{40, 10, 30, 20} {
		value, _ := sqltype.Float(float8, n)
		tuples = append(tuples, value, sqltype.Value{})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &sortCancelContext{Context: ctx, cancel: cancel}
	call := plan.AggregateCall{StageWidth: 2, Order: []plan.SortKey{{Comparison: comparison, Cache: 0}}}
	if _, err := sortOrderedAggregate(probe, &queryState{}, call, tuples); !probe.entered || !errors.Is(err, context.Canceled) {
		t.Fatal(probe.entered, err)
	}
}
