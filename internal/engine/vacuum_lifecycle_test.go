package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"testing"
)

func vacuumLateCompletion(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE late(id INT)")
	sessionSQL(t, s, "INSERT INTO late VALUES(1),(2)")
	sessionSQL(t, s, "DELETE FROM late WHERE id=1")
	_, store := vacuumStore(t, e, "late")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "VACUUM late"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	cancel()
	if err := scope.Finish(nil); err != nil {
		t.Fatal("late cancellation retracted installed completion", err)
	}
	if result.Count != 0 || result.CommandTag != "VACUUM" || store.Maintenance.VacuumCount != 1 || len(store.Versions) != 1 {
		t.Fatal("installed completion lost", result, store.Maintenance)
	}
	lockEmpty(t, e)
}
func vacuumReplacementWait(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "Reset", true: "Restore"}[restore], func(t *testing.T) {
			e := New()
			owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE waiting(id INT)", "INSERT INTO waiting VALUES(1),(2)", "DELETE FROM waiting WHERE id=1"} {
				sessionSQL(t, owner, q)
			}
			ref, old := vacuumStore(t, e, "waiting")
			root, ends, stats := old.Versions, old.Ends, old.Maintenance
			transactionBegin(t, holder)
			if err := e.locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.Share); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, worker, ctx, "VACUUM waiting", false)
			joined := false
			defer func() {
				cancel()
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			lockPending(t, e, 1)
			var err error
			if restore {
				err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE waiting(id INT); INSERT INTO waiting VALUES(9);"))
			} else {
				err = e.Reset(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			lockAwait(t, done, ids.ErrCatalogReplaced)
			joined = true
			if !storage.SameVersionRoot(root, old.Versions) || old.Ends != ends || old.Maintenance != stats {
				t.Fatal("replacement wait installed stale candidate")
			}
			sessionSQL(t, holder, "ROLLBACK")
			if !restore {
				sessionSQL(t, worker, "CREATE TABLE waiting(id INT)")
			}
			_, fresh := vacuumStore(t, e, "waiting")
			if fresh == old || fresh.Maintenance.VacuumCount != 0 {
				t.Fatal("same-name replacement inherited maintenance")
			}
			sessionSQL(t, worker, "VACUUM waiting")
			if fresh.Maintenance.VacuumCount != 1 {
				t.Fatal("replacement recovery unhealthy")
			}
			e.mu.Lock()
			active := len(e.vacuumProgress)
			e.mu.Unlock()
			if active != 0 {
				t.Fatal("replacement leaked progress")
			}
			lockEmpty(t, e)
		})
	}
}
