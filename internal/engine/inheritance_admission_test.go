package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestInheritanceAdmissionDropLateGrowthAndCancel(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "growth", true: "cancel"}[cancelWait], func(t *testing.T) {
			e := New()
			parent := inheritanceLifecycleTable(e, 20000, "p")
			firstChild := inheritanceLifecycleTable(e, 20001, "first", parent.Address())
			inheritanceLifecycleInstall(e, parent, firstChild)
			holder, holderDone := roleOperation(t, e, "postgres")
			defer holderDone()
			holderRef, err := e.Begin(holder, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			base := e.CatalogRead()
			ref := e.Catalog.Domain.Ref(base.Epoch(), parent.Address())
			if err = e.Locks.Acquire(context.Background(), holder.Session.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.ShareUpdateExclusive); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			tx, err := e.Begin(op, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			scope := &StatementScope{Op: op, Owner: op.Session.Transactions, TxRef: tx, LockTicket: op.Session.Transactions.Ticket}
			defer func() {
				if op.Context().Err() != nil {
					// A canceled Commit follows the real failed-commit rollback
					// path and releases the transaction's granted locks.
					if _, err := e.Commit(op, tx); !errors.Is(err, context.Canceled) {
						t.Error(err)
					}
				} else if _, err := e.Rollback(op, tx); err != nil {
					t.Error(err)
				}
				if cancelWait {
					if _, err := e.Rollback(holder, holderRef); err != nil {
						t.Error(err)
					}
				}
				lockEmpty(t, e)
			}()
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if err = stage.Remove(ref); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			joined := false
			defer func() {
				if !joined {
					cancel()
					select {
					case <-result:
					case <-time.After(5 * time.Second):
						t.Error("drop admission worker did not terminate")
					}
				}
			}()
			go func() {
				result <- DropProviderLockInheritanceClosure(StageDropProvider(nil, stage, scope, nil, nil), []ids.ObjectAddress{parent.Address()})
			}()
			lockPending(t, e, 1)
			if cancelWait {
				cancel()
			} else {
				// Publish immutable metadata while the older SUE ticket owns the
				// parent. The queued drop must see this child after receiving AX.
				child := inheritanceLifecycleTable(e, 20002, "late", firstChild.Address())
				inheritanceLifecycleInstall(e, child)
				if _, err = e.Rollback(holder, holderRef); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err = <-result:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("drop admission did not finish")
			}
			if cancelWait {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if stage.Base.State() != base.State() || stage.RemovalCount() != 1 {
					t.Fatal("cancel changed staged closure")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if stage.Base.State() == base.State() || len(catalog.InheritanceChildren(stage.Base, firstChild.Address())) != 1 {
					t.Fatal("late child omitted")
				}
				if err = StageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{parent.Address()}, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err == nil {
					t.Fatal("late child did not block RESTRICT")
				}
				if err = StageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{parent.Address()}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
					t.Fatal(err)
				}
				if stage.RemovalCount() != 3 {
					t.Fatal("late child not included in CASCADE")
				}
			}
		})
	}
}

func TestInheritanceAdmissionStandaloneDropKeepsExistingLockPolicy(t *testing.T) {
	e := New()
	table := inheritanceLifecycleTable(e, 20000, "standalone")
	inheritanceLifecycleInstall(e, table)
	holder, holderDone := roleOperation(t, e, "postgres")
	defer holderDone()
	holderTx, err := e.Begin(holder, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := e.Rollback(holder, holderTx); err != nil {
			t.Error(err)
		}
		lockEmpty(t, e)
	}()
	base := e.CatalogRead()
	ref := e.Catalog.Domain.Ref(base.Epoch(), table.Address())
	if err := e.Locks.Acquire(context.Background(), holder.Session.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if op.Context().Err() != nil {
			if _, err := e.Commit(op, tx); !errors.Is(err, context.DeadlineExceeded) {
				t.Error(err)
			}
		} else if _, err := e.Rollback(op, tx); err != nil {
			t.Error(err)
		}
	}()
	scope := &StatementScope{Op: op, Owner: op.Session.Transactions, TxRef: tx, LockTicket: op.Session.Transactions.Ticket}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Remove(ref); err != nil {
		t.Fatal(err)
	}
	if err := DropProviderLockInheritanceClosure(StageDropProvider(nil, stage, scope, nil, nil), []ids.ObjectAddress{table.Address()}); err != nil {
		t.Fatal("standalone DROP attempted an extra root grant", err)
	}
	lockState := e.Locks.Inspect(scope.LockTicket)
	resources := len(lockState.Journals[scope.LockTicket])
	if resources != 0 || stage.Base.State() != base.State() || stage.RemovalCount() != 1 {
		t.Fatal("standalone closure changed lock ownership or staged catalog")
	}
}
