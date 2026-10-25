package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func rlsBaseLockCancellation(t *testing.T, e *core.Engine, d *catalog.Relation) {
	t.Helper()
	holder := sessionStart(t, e, nil)
	waiter := sessionStart(t, e, nil)
	tx := transactionBegin(t, holder)
	ref := catalogRef(t, e, e.CatalogRead(), d.Name())
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		transactionEnd(t, holder, tx, false)
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
	}()
	go func() {
		op, err := waiter.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Actions: []parser.AlterTableAction{{Action: parser.DisableRowSecurity}}}, 0, nil)
			op.Done()
		}
		done <- err
	}()
	lockPending(t, e, 1)
	cancel()
	lockAwait(t, done, context.Canceled)
	joined = true
	current := e.CatalogRead().MustRelation(d.Address())
	if current != d || current.Security.RLSEnabled || current.Security.RLSForced {
		t.Fatal("canceled no-op changed immutable descriptor")
	}
}
