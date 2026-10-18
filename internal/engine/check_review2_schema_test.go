package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

const checkReview2SchemaCreate = "CREATE SCHEMA checked CREATE TABLE earlier(a INT CHECK(a=1)) CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('public.s')>0))"

func checkReview2SchemaAbsent(t *testing.T, e *Engine) {
	t.Helper()
	read := e.CatalogRead()
	for _, d := range read.state.descriptors {
		if d.Address().ClassID == NamespaceClassID && d.Name() == "checked" {
			t.Fatal("failed inline CREATE published its schema")
		}
		if r, ok := d.(*relationDescriptor); ok && (r.name == "earlier" || r.name == "t") {
			t.Fatal("failed inline CREATE published a table", r.name)
		}
	}
}

// Observe the actual manager queue before canceling. The completion channel also
// makes a bypass fail immediately instead of waiting for a guessed delay.
func checkReview2SchemaQueued(t *testing.T, e *Engine, holder *Session, ref ids.ObjectRef, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		entry := e.locks.entries[lockResource{ObjectRef: ref}]
		queued := entry != nil && len(entry.pending) == 1 && entry.pending[0].mode == accessShare && entry.holders[holder.transactions.ticket].mode&accessExclusive != 0
		e.locks.mu.Unlock()
		if queued {
			lockPending(t, e, 1)
			return
		}
		select {
		case err := <-done:
			t.Fatalf("inline CREATE completed without AS queue on exact public.s behind private DROP AX: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("inline CREATE did not queue AS on exact public.s behind private DROP AX")
		}
		runtime.Gosched()
	}
}

func TestCheckReview2SchemaProviderAdmission(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE SEQUENCE public.s")
				ref := catalogRef(t, e, e.CatalogRead(), "s")
				tx := transactionBegin(t, a)
				sessionSQL(t, a, "DROP SEQUENCE public.s CASCADE")
				if prepared {
					lockAwait(t, lockAsync(e, b, context.Background(), checkReview2SchemaCreate, true), nil)
					checkReview2SchemaAbsent(t, e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				waiting := lockAsync(e, b, ctx, checkReview2SchemaCreate, false)
				checkReview2SchemaQueued(t, e, a, ref, waiting)
				checkReview2SchemaAbsent(t, e)
				cancel()
				lockAwait(t, waiting, context.Canceled)
				lockPending(t, e, 0)
				checkReview2SchemaAbsent(t, e)
				checkReviewDependencyValid(t, e)

				// A new execution must wait too, then resolve the captured provider
				// after the deleting transaction ends, without retargeting its name.
				retryCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				waiting = lockAsync(e, b, retryCtx, checkReview2SchemaCreate, false)
				checkReview2SchemaQueued(t, e, a, ref, waiting)
				transactionEnd(t, a, tx, commit)
				if commit {
					select {
					case err := <-waiting:
						if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("inline CREATE did not reject committed-away provider after grant", err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("inline CREATE did not resume after DROP commit")
					}
					checkReview2SchemaAbsent(t, e)
					if err := e.ValidateObjectRef(e.CatalogRead(), ref); err == nil {
						t.Fatal("DROP commit retained provider")
					}
					checkReviewDependencyValid(t, e)
					sessionSQL(t, b, "CREATE SEQUENCE public.s")
					sessionSQL(t, b, checkReview2SchemaCreate)
				} else {
					lockAwait(t, waiting, nil)
					if err := e.ValidateObjectRef(e.CatalogRead(), ref); err != nil {
						t.Fatal("DROP rollback lost original provider", err)
					}
				}
				sessionSQL(t, b, "INSERT INTO checked.earlier VALUES(1)")
				sessionSQL(t, b, "INSERT INTO checked.t VALUES(7)")
				checkReviewDependencyValid(t, e)
				lockEmpty(t, e)
			})
		}
	}
}
