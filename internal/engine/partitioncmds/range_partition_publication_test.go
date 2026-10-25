package partitioncmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
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

// Cancel on real private progress, independently of executor function names or
// the number of context checks. No other goroutine mutates this owned stage.
type partitionStagedCancelContext struct {
	context.Context
	scope         *engine.StatementScope
	first, second ids.ObjectAddress
	cancel        context.CancelFunc
	entered       atomic.Bool
}

func (c *partitionStagedCancelContext) Err() error {
	if !c.entered.Load() && len(c.scope.Stage.Rows[c.first]) != 0 && len(c.scope.Stage.Rows[c.second]) == 0 && c.entered.CompareAndSwap(false, true) {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRangePartitionPublicationCancelAtMultiLeafApply(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE root(k INT,v INT) PARTITION BY RANGE(k)",
		"CREATE TABLE low PARTITION OF root(v UNIQUE) FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE high PARTITION OF root(v UNIQUE) FOR VALUES FROM(10) TO(20)",
		"INSERT INTO root VALUES(1,10),(11,20)",
	} {
		enginetest.SessionSQL(t, session, query)
	}
	underlying, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := rangePartitionRelation(t, e, "low"), rangePartitionRelation(t, e, "high")
	op, err := session.Acquire(underlying)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	ctx := &partitionStagedCancelContext{Context: op.Context(), scope: scope, first: first.Address(), second: second.Address(), cancel: cancel}
	op.Ctx = ctx
	_, err = scope.Execute(enginetest.Statement(t, "INSERT INTO root VALUES(2,21),(12,22)"), 0, nil)
	err = scope.Finish(err)
	op.Done()
	if !ctx.entered.Load() || !errors.Is(err, context.Canceled) {
		t.Fatal("private first-leaf progress did not observe cancellation", ctx.entered.Load(), err)
	}
	expected := [][]driver.Value{{int64(1), int64(10)}, {int64(11), int64(20)}}
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), expected) {
		t.Fatal("canceled staged apply installed a destination group")
	}
	enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
		t.Fatal("canceled unique stages leaked provisional claims")
	}
	enginetest.LockEmpty(t, e)
}

func TestRangePartitionPublicationResetDuringBoundDemand(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	var resetErr error
	ctx := &rangePartitionPhaseContext{Context: context.Background(), target: "(*pendingPartitionDefinition).finish", effect: func() { resetErr = engine.Reset(context.Background(), e) }}
	op, err := session.Acquire(ctx.Context)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Context = op.Context()
	op.Ctx = ctx
	_, err = e.Execute(op, enginetest.Statement(t, "CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(10)"), 0, nil)
	op.Done()
	if !ctx.entered.Load() || resetErr != nil || err == nil {
		t.Fatal("control callback failed to invalidate private DDL publication", ctx.entered.Load(), resetErr, err)
	}
	e.Mu.Lock()
	storage := len(e.Storage.Relations)
	for _, descriptor := range e.Catalog.State.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && (relation.Name() == "root" || relation.Name() == "leaf") {
			t.Error("stale hierarchy definition published after reset")
		}
	}
	e.Mu.Unlock()
	if storage != 0 {
		t.Fatal("reset retained physical storage", storage)
	}
	enginetest.SessionSQL(t, session, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	enginetest.SessionSQL(t, session, "CREATE TABLE leaf PARTITION OF root DEFAULT")
	enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(1)")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k FROM root")), [][]driver.Value{{int64(1)}}) {
		t.Fatal("reset callback poisoned later admission")
	}
	enginetest.LockEmpty(t, e)
}
