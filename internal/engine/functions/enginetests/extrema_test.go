package enginetests_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
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
		rows, count, err := exec.Finalize(probe, query.Root, source, nil)
		cancel()
		if !probe.entered || err != context.Canceled || rows != nil || count != 0 || source.View != nil || source.Anonymous != nil {
			t.Fatal("actual comparator publication/ownership", probe.entered, rows, count, err, source)
		}
	}
	b, source := insertSelectEvaluationFixture(t, "SELECT min(v) FROM t")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &extremaCompareCancelContext{Context: ctx, cancel: cancel}
	rows, err := dml.EvaluateInsertQuery(probe, b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if !probe.entered || rows.Values != nil || err != context.Canceled || source.View != nil || source.Anonymous != nil {
		t.Fatal(rows, err, probe.entered, source)
	}
}

func TestExtremaEagerEvaluationAndSlots(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT min(v),min(v),max(v) FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 2 || node.Calls[0].Operation != functions.AggregateMin || node.Calls[1].Operation != functions.AggregateMax {
		t.Fatal(node.Calls)
	}
	for _, c := range query.Root.Schema() {
		if !c.NullabilityKnown || !c.Nullable || c.Usage.Typmod != -1 || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
	original := node.Calls[0].Arguments[0]
	calls := 0
	node.Calls[0].Arguments[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return original.Eval(c) })
	rows, _, err := exec.Finalize(context.Background(), query.Root, source, nil)
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
	defer owner.Close()
	if len(volatile.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode).Calls) != 3 {
		t.Fatal("volatile calls shared")
	}
	_, failed, failedSource := queryCoreFixture(t, "SELECT min(v) FROM t")
	failedNode := failed.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	argument := failedNode.Calls[0].Arguments[0]
	sentinel := errors.New("late extrema argument")
	calls = 0
	failedNode.Calls[0].Arguments[0] = queryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 4 {
			return sqltype.Value{}, sentinel
		}
		return argument.Eval(c)
	})
	result, count, err := exec.Finalize(context.Background(), failed.Root, failedSource, nil)
	if result != nil || count != 0 || err != sentinel || calls != 4 || failedSource.View != nil || failedSource.Anonymous != nil {
		t.Fatal(result, count, err, calls, failedSource)
	}
}

func insertSelectEvaluationFixture(t *testing.T, sql string) (analyze.Binding, *exec.Source) {
	t.Helper()
	e, q, source := queryCoreFixture(t, sql)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	e.Mu.Lock()
	relation := e.Catalog.State.MustRelation(ref.Address)
	e.Mu.Unlock()
	return analyze.Binding{Query: q, Relation: relation, Ref: ref, Targets: []int{0}}, source
}
