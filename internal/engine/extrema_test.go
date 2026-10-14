package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

// Detect the actual shared comparator on the stack, not an Err-call count or a
// mock provider. Cancel inside Compare's own context checkpoint deterministically.
type extremaCompareCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	entered bool
}

func (c *extremaCompareCancelContext) Err() error {
	if !c.entered {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".Comparison.Compare") {
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

func TestExtremaActualComparatorCancellationOwnership(t *testing.T) {
	for _, maximum := range []bool{false, true} {
		name := "min"
		if maximum {
			name = "max"
		}
		_, query, source := queryCoreFixture(t, "SELECT "+name+"(v) FROM t")
		ctx, cancel := context.WithCancel(context.Background())
		probe := &extremaCompareCancelContext{Context: ctx, cancel: cancel}
		rows, count, err := finalizeQuery(probe, query.Root, source, nil)
		cancel()
		if !probe.entered || err != context.Canceled || rows != nil || count != 0 || source.view != nil || source.anonymous != nil {
			t.Fatal("actual comparator publication/ownership", probe.entered, rows, count, err, source)
		}
	}
	b, source := insertSelectEvaluationFixture(t, "SELECT min(v) FROM t")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &extremaCompareCancelContext{Context: ctx, cancel: cancel}
	rows, err := evaluateInsertQuery(probe, b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if !probe.entered || rows.values != nil || err != context.Canceled || source.view != nil || source.anonymous != nil {
		t.Fatal(rows, err, probe.entered, source)
	}
}

func TestExtremaEagerEvaluationAndSlots(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT min(v),min(v),max(v) FROM t")
	node := query.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 2 || node.calls[0].operation != aggregateMin || node.calls[1].operation != aggregateMax {
		t.Fatal(node.calls)
	}
	for _, c := range query.Root.Schema() {
		if !c.NullabilityKnown || !c.Nullable || c.Usage.Typmod != -1 || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
	original := node.calls[0].argument
	calls := 0
	node.calls[0].argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { calls++; return original.Eval(c) })
	rows, _, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	before := calls
	if before != 4 {
		t.Fatal("later arguments skipped", calls)
	}
	values := make([]sqltype.Value, 3)
	if ok, err := rows.Next(context.Background(), values); !ok || err != nil || calls != before {
		t.Fatal("public Next evaluated", ok, err, calls)
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT min(nextval($1)),min(nextval($1)),max(nextval($1)) FROM t")
	defer owner.close()
	if len(volatile.Root.(*projectNode).child.(*aggregateNode).calls) != 3 {
		t.Fatal("volatile calls shared")
	}
	_, failed, failedSource := queryCoreFixture(t, "SELECT min(v) FROM t")
	failedNode := failed.Root.(*projectNode).child.(*aggregateNode)
	argument := failedNode.calls[0].argument
	sentinel := errors.New("late extrema argument")
	calls = 0
	failedNode.calls[0].argument = queryProbe(argument.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 4 {
			return sqltype.Value{}, sentinel
		}
		return argument.Eval(c)
	})
	result, count, err := finalizeQuery(context.Background(), failed.Root, failedSource, nil)
	if result != nil || count != 0 || err != sentinel || calls != 4 || failedSource.view != nil || failedSource.anonymous != nil {
		t.Fatal(result, count, err, calls, failedSource)
	}
}
