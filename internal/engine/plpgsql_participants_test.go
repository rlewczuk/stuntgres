package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
)

type plExceptionFaultTx struct {
	fdwUnitTransaction
	saveError, rollbackError error
	canceledRollback         bool
}

func (x *plExceptionFaultTx) Savepoint(_ context.Context, name string) error {
	x.record("save:" + name)
	return x.saveError
}

func (x *plExceptionFaultTx) RollbackTo(ctx context.Context, name string) error {
	x.canceledRollback = ctx.Err() != nil
	x.record("rollback:" + name)
	return x.rollbackError
}

func plExceptionUnlocked(t *testing.T, e *core.Engine, s *core.StatementScope) func() {
	return func() {
		t.Helper()
		if !e.Mu.TryLock() {
			t.Error("provider callback holds engine publication lock")
		} else {
			e.Mu.Unlock()
		}
		if !s.Owner.Mu.TryLock() {
			t.Error("provider callback holds owner lock")
		} else {
			s.Owner.Mu.Unlock()
		}
		if !s.Op.Session.Mu.TryLock() {
			t.Error("provider callback holds session lock")
		} else {
			s.Op.Session.Mu.Unlock()
		}
		if !e.ReplacementMu.TryLock() {
			t.Error("provider callback holds replacement reservation")
		} else {
			e.ReplacementMu.Unlock()
		}
	}
}
