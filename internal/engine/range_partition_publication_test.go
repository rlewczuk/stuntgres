package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// Observe a real family checkpoint, not whole-pipeline Err counts or a fake
// scalar provider. Cancellation and reset still use their actual authorities.
type rangePartitionPhaseContext struct {
	context.Context
	target  string
	effect  func()
	entered atomic.Bool
}

func (c *rangePartitionPhaseContext) Err() error {
	if !c.entered.Load() {
		var pcs [48]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if strings.Contains(frame.Function, c.target) {
				if c.entered.CompareAndSwap(false, true) && c.effect != nil {
					c.effect()
				}
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestRangePartitionPublicationCancelAtMultiLeafApply(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE root(k INT,v INT) PARTITION BY RANGE(k)",
		"CREATE TABLE low PARTITION OF root(v UNIQUE) FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE high PARTITION OF root(v UNIQUE) FOR VALUES FROM(10) TO(20)",
		"INSERT INTO root VALUES(1,10),(11,20)",
	} {
		sessionSQL(t, session, query)
	}
	underlying, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &rangePartitionPhaseContext{Context: underlying, target: "(*StatementScope).applyInsertValidated", effect: cancel}
	op, err := session.Acquire(ctx.Context)
	if err != nil {
		t.Fatal(err)
	}
	// Acquire wraps its input with WithCancel; observe the actual operation
	// context so the real family checkpoints reach this callback.
	ctx.Context = op.Context()
	op.ctx = ctx
	_, err = e.Execute(op, statement(t, "INSERT INTO root VALUES(2,21),(12,22)"), 0, nil)
	op.Done()
	if !ctx.entered.Load() || !errors.Is(err, context.Canceled) {
		t.Fatal("real multi-leaf apply did not observe cancellation", ctx.entered.Load(), err)
	}
	expected := [][]driver.Value{{int64(1), int64(10)}, {int64(11), int64(20)}}
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), expected) {
		t.Fatal("canceled staged apply installed a destination group")
	}
	sessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
		t.Fatal("canceled unique stages leaked provisional claims")
	}
	lockEmpty(t, e)
}

func TestRangePartitionPublicationResetDuringBoundDemand(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	var resetErr error
	ctx := &rangePartitionPhaseContext{Context: context.Background(), target: "(*pendingPartitionDefinition).finish", effect: func() { resetErr = e.Reset(context.Background()) }}
	op, err := session.Acquire(ctx.Context)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Context = op.Context()
	op.ctx = ctx
	_, err = e.Execute(op, statement(t, "CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(10)"), 0, nil)
	op.Done()
	if !ctx.entered.Load() || resetErr != nil || err == nil {
		t.Fatal("control callback failed to invalidate private DDL publication", ctx.entered.Load(), resetErr, err)
	}
	e.mu.Lock()
	storage := len(e.storage)
	for _, descriptor := range e.state.descriptors {
		if relation, ok := descriptor.(*relationDescriptor); ok && (relation.name == "root" || relation.name == "leaf") {
			t.Error("stale hierarchy definition published after reset")
		}
	}
	e.mu.Unlock()
	if storage != 0 {
		t.Fatal("reset retained physical storage", storage)
	}
	sessionSQL(t, session, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	sessionSQL(t, session, "CREATE TABLE leaf PARTITION OF root DEFAULT")
	sessionSQL(t, session, "INSERT INTO root VALUES(1)")
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k FROM root")), [][]driver.Value{{int64(1)}}) {
		t.Fatal("reset callback poisoned later admission")
	}
	lockEmpty(t, e)
}
