package tablecmds_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func serialPublicationQuery(alter bool) string {
	if alter {
		return `ALTER TABLE consumer ALTER id SET DEFAULT nextval('q')`
	}
	return `CREATE TABLE consumer(id INT DEFAULT nextval('q'))`
}

func TestSerialDefaultUnpublishedProviderHold(t *testing.T) {
	for _, alter := range []bool{false, true} {
		t.Run(fmt.Sprint(alter), func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, `CREATE SEQUENCE q`)
			if alter {
				enginetest.SessionSQL(t, a, `CREATE TABLE consumer(id SERIAL)`)
			}
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "q")
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var gated atomic.Bool
			e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{e.Catalog.Providers[ids.RelationClassID], func() {
				if gated.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}}
			creating := enginetest.LockAsync(e, a, context.Background(), serialPublicationQuery(alter), false)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("default validation gate not reached")
			}
			dropping := enginetest.LockAsync(e, b, context.Background(), `DROP SEQUENCE q RESTRICT`, false)
			deadline := time.Now().Add(5 * time.Second)
			for {
				select {
				case err := <-dropping:
					unblock()
					enginetest.LockAwait(t, creating, nil)
					t.Fatalf("DROP reached publication before unpublished default released its provider: %v", err)
				default:
				}
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessExclusive
				held := false
				if entry != nil {
					for _, mode := range entry.Holders {
						held = held || mode.Mode&lock.AccessShare != 0
					}
				}
				if queued {
					if !held {
						t.Fatal("default lacks retained provider AS")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("DROP neither queued nor completed")
				}
				runtime.Gosched()
			}
			unblock()
			enginetest.LockAwait(t, creating, nil)
			select {
			case err := <-dropping:
				enginetest.State(t, err, "2BP01")
			case <-time.After(5 * time.Second):
				t.Fatal("DROP did not resume")
			}
			enginetest.SessionSQL(t, a, `INSERT INTO consumer DEFAULT VALUES`)
			sequenceScalar(t, a, `SELECT id FROM consumer`, 1)
			checkReviewDependencyValid(t, e)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestSerialDefaultProviderDropFirstAndCancellation(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/commit=%t", alter, commit), func(t *testing.T) {
				e := engine.New()
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, `CREATE SEQUENCE q`)
				if alter {
					enginetest.SessionSQL(t, a, `CREATE TABLE consumer(id SERIAL)`)
				}
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "q")
				tx := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, `DROP SEQUENCE q RESTRICT`)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waiting := enginetest.LockAsync(e, b, ctx, serialPublicationQuery(alter), false)
				enginetest.LockPending(t, e, 1)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare
				if !queued {
					t.Fatal("default did not queue on captured provider")
				}
				cancel()
				enginetest.LockAwait(t, waiting, context.Canceled)
				waiting = enginetest.LockAsync(e, b, context.Background(), serialPublicationQuery(alter), false)
				enginetest.LockPending(t, e, 1)
				enginetest.TransactionEnd(t, a, tx, commit)
				if commit {
					select {
					case err := <-waiting:
						if err == nil {
							t.Fatal("default published deleted provider")
						}
					case <-time.After(5 * time.Second):
						t.Fatal("default did not resume")
					}
					if !alter {
						enginetest.SessionSQL(t, b, `CREATE TABLE consumer(id SERIAL)`)
					}
				} else {
					enginetest.LockAwait(t, waiting, nil)
				}
				enginetest.SessionSQL(t, b, `INSERT INTO consumer DEFAULT VALUES`)
				sequenceScalar(t, b, `SELECT id FROM consumer`, 1)
				checkReviewDependencyValid(t, e)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
