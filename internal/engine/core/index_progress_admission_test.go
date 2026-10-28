package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func indexProgressAdmission(t *testing.T) {
	e := engine.New()
	writer := sessionStart(t, e, nil)
	builder := sessionStart(t, e, nil)
	observer := transactionOperation(t, sessionStart(t, e, nil))
	defer observer.Done()
	sessionSQL(t, writer, "CREATE TABLE progress_admission(k INT)")
	tx := enginetest.TransactionBegin(t, writer)
	defer enginetest.TransactionEnd(t, writer, tx, false)
	sessionSQL(t, writer, "LOCK TABLE progress_admission IN ROW EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := enginetest.LockAsync(e, builder, ctx, "CREATE INDEX progress_admission_idx ON progress_admission(k)", false)
	joined := false
	defer func() {
		if !joined {
			cancel()
			enginetest.LockAwait(t, done, context.Canceled)
		}
	}()
	enginetest.LockPending(t, e, 1)
	for _, entry := range e.Locks.Inspect().Resources {
		for _, pending := range entry.Pending {
			if pending.Mode != lock.Share {
				t.Fatal("ordinary admission is not ShareLock", pending.Mode)
			}
		}
	}
	if facts := indexProgressFacts(t, e, observer); len(facts) != 0 {
		t.Fatal("initial admission falsely reported build work", facts)
	}
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	joined = true
	if facts := indexProgressFacts(t, e, observer); len(facts) != 0 {
		t.Fatal("blocked cancellation retained progress", facts)
	}
}
