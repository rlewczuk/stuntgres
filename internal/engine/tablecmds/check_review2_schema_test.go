package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

const checkReview2SchemaCreate = "CREATE SCHEMA checked CREATE TABLE earlier(a INT CHECK(a=1)) CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('public.s')>0))"

func checkReview2SchemaAbsent(t *testing.T, e *engine.Engine) {
	t.Helper()
	read := e.CatalogRead()
	for _, d := range read.Descriptors() {
		if d.Address().ClassID == ids.NamespaceClassID && d.Name() == "checked" {
			t.Fatal("failed inline CREATE published its schema")
		}
		if r, ok := d.(*catalog.Relation); ok && (r.Name() == "earlier" || r.Name() == "t") {
			t.Fatal("failed inline CREATE published a table", r.Name())
		}
	}
}

// Observe the actual manager queue before canceling. The completion channel also
// makes a bypass fail immediately instead of waiting for a guessed delay.
func checkReview2SchemaQueued(t *testing.T, e *engine.Engine, holder *session.Session, ref ids.ObjectRef, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.Transactions.Ticket].Mode&lock.AccessExclusive != 0
		if queued {
			enginetest.LockPending(t, e, 1)
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
				e := engine.New()
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE SEQUENCE public.s")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
				tx := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "DROP SEQUENCE public.s CASCADE")
				if prepared {
					enginetest.LockAwait(t, enginetest.LockAsync(e, b, context.Background(), checkReview2SchemaCreate, true), nil)
					checkReview2SchemaAbsent(t, e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				waiting := enginetest.LockAsync(e, b, ctx, checkReview2SchemaCreate, false)
				checkReview2SchemaQueued(t, e, a, ref, waiting)
				checkReview2SchemaAbsent(t, e)
				cancel()
				enginetest.LockAwait(t, waiting, context.Canceled)
				enginetest.LockPending(t, e, 0)
				checkReview2SchemaAbsent(t, e)
				checkReviewDependencyValid(t, e)

				// A new execution must wait too, then resolve the captured provider
				// after the deleting transaction ends, without retargeting its name.
				retryCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				waiting = enginetest.LockAsync(e, b, retryCtx, checkReview2SchemaCreate, false)
				checkReview2SchemaQueued(t, e, a, ref, waiting)
				enginetest.TransactionEnd(t, a, tx, commit)
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
					if err := enginetest.ValidateObjectRef(e, e.CatalogRead(), ref); err == nil {
						t.Fatal("DROP commit retained provider")
					}
					checkReviewDependencyValid(t, e)
					enginetest.SessionSQL(t, b, "CREATE SEQUENCE public.s")
					enginetest.SessionSQL(t, b, checkReview2SchemaCreate)
				} else {
					enginetest.LockAwait(t, waiting, nil)
					if err := enginetest.ValidateObjectRef(e, e.CatalogRead(), ref); err != nil {
						t.Fatal("DROP rollback lost original provider", err)
					}
				}
				enginetest.SessionSQL(t, b, "INSERT INTO checked.earlier VALUES(1)")
				enginetest.SessionSQL(t, b, "INSERT INTO checked.t VALUES(7)")
				checkReviewDependencyValid(t, e)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
