package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestCTEDMLPendingCancellation(t *testing.T)         { cteDMLPendingBoundary(t, false) }
func TestCTEDMLTargetReplacementDuringWait(t *testing.T) { cteDMLPendingBoundary(t, true) }

func cteDMLPendingBoundary(t *testing.T, replacement bool) {
	t.Helper()
	e := New()
	holder, worker, peer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT UNIQUE,v INT)", "CREATE TABLE audit(id INT)", "INSERT INTO t VALUES(1,10)"} {
		sessionSQL(t, peer, q)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	audit := catalogRef(t, e, e.CatalogRead(), "audit")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "UPDATE t SET v=99 WHERE id=1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ast := statement(t, "WITH a AS (INSERT INTO audit VALUES(1) RETURNING id), u AS (UPDATE t SET v=t.v+1 FROM a WHERE t.id=a.id RETURNING t.id,t.v) SELECT * FROM u")
	type completion struct {
		result Result
		err    error
		scope  *StatementScope
	}
	done, ready, joined := make(chan completion, 1), make(chan *StatementScope, 1), make(chan struct{})
	go func() {
		defer close(joined)
		op, err := worker.Acquire(ctx)
		var result Result
		var scope *StatementScope
		if err == nil {
			scope, err = e.StartStatement(op)
			ready <- scope
			if err == nil {
				result, err = scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
				if err != nil {
					if result.Rows != nil {
						result.Rows.Close()
					}
					result = Result{}
				}
			}
			op.Done()
		} else {
			ready <- nil
		}
		done <- completion{result, err, scope}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("CTE pending worker did not retire")
		}
		if holder.TransactionState() != session.TxIdle {
			transactionEnd(t, holder, tx, false)
		}
	})
	var scope *StatementScope
	select {
	case scope = <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if scope == nil {
		t.Fatal("missing worker scope")
	}
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	// The manager mutex's enqueue/observation ordering publishes this earlier
	// private stage while the exact original-row worker remains blocked.
	if len(scope.Stage.Rows[audit.Address]) != 1 || scope.View == nil || scope.LockTicket == nil || ScopeCTEInvocation(scope) == nil {
		t.Fatal("wait preceded the required earlier real mutation")
	}
	view := scope.View.Load()
	if view == nil {
		t.Fatal("pending statement lost its shared view")
	}
	if _, ok := view.Committed[audit.Address]; !ok {
		t.Fatal("earlier target absent from shared view")
	}
	if _, ok := view.Committed[ref.Address]; !ok {
		t.Fatal("later target absent from shared view")
	}
	transactionRows(t, peer, "SELECT id FROM audit")
	transactionRows(t, peer, "SELECT v FROM t", 10)
	want := error(context.Canceled)
	if replacement {
		// Exercise the genuine physical-storage fence while a different target
		// is active/pending. The same SQL identity must not authorize a new root.
		e.Mu.Lock()
		old := e.Storage.Relations[audit.Address]
		e.Storage.InstallRelationLocked(audit.Address, &storage.Relation{Versions: old.Versions, Ends: old.Ends, Generation: old.Generation})
		e.Mu.Unlock()
		want = ids.ErrCatalogReplaced
		transactionEnd(t, holder, tx, false)
	} else {
		cancel()
	}
	select {
	case out := <-done:
		if !errors.Is(out.err, want) || out.result.Rows != nil || out.result.Count != 0 || out.scope != scope {
			t.Fatal(out.result, out.err, want)
		}
		if scope.View != nil || scope.LockTicket != nil || scope.Stage.Rows != nil || scope.Stage.Deletes != nil || ScopeMutations(scope) != nil || ScopeCTEInvocation(scope) != nil || !scope.Finished {
			t.Fatal("cancellation retained stage/view/target/producer ownership")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending cancellation did not finish")
	}
	if !replacement {
		transactionEnd(t, holder, tx, false)
	}
	transactionRows(t, peer, "SELECT id FROM audit")
	transactionRows(t, peer, "SELECT v FROM t", 10)
	lockEmpty(t, e)
	sessionSQL(t, peer, "INSERT INTO audit VALUES(2)")
	sessionSQL(t, peer, "UPDATE t SET v=v+1")
	transactionRows(t, peer, "SELECT v FROM t", 11)
	lockEmpty(t, e)
}
