package engine

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, `CREATE SEQUENCE q`)
			if alter {
				sessionSQL(t, a, `CREATE TABLE consumer(id SERIAL)`)
			}
			ref := catalogRef(t, e, e.CatalogRead(), "q")
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var gated atomic.Bool
			e.catalog.providers[ids.RelationClassID] = alterDropProvider{e.catalog.providers[ids.RelationClassID], func() {
				if gated.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}}
			creating := lockAsync(e, a, context.Background(), serialPublicationQuery(alter), false)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("default validation gate not reached")
			}
			dropping := lockAsync(e, b, context.Background(), `DROP SEQUENCE q RESTRICT`, false)
			deadline := time.Now().Add(5 * time.Second)
			for {
				select {
				case err := <-dropping:
					unblock()
					lockAwait(t, creating, nil)
					t.Fatalf("DROP reached publication before unpublished default released its provider: %v", err)
				default:
				}
				lockState := e.locks.Inspect()
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
			lockAwait(t, creating, nil)
			select {
			case err := <-dropping:
				state(t, err, "2BP01")
			case <-time.After(5 * time.Second):
				t.Fatal("DROP did not resume")
			}
			sessionSQL(t, a, `INSERT INTO consumer DEFAULT VALUES`)
			sequenceScalar(t, a, `SELECT id FROM consumer`, 1)
			checkReviewDependencyValid(t, e)
			lockEmpty(t, e)
		})
	}
}

func TestSerialDefaultProviderDropFirstAndCancellation(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/commit=%t", alter, commit), func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, `CREATE SEQUENCE q`)
				if alter {
					sessionSQL(t, a, `CREATE TABLE consumer(id SERIAL)`)
				}
				ref := catalogRef(t, e, e.CatalogRead(), "q")
				tx := transactionBegin(t, a)
				sessionSQL(t, a, `DROP SEQUENCE q RESTRICT`)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waiting := lockAsync(e, b, ctx, serialPublicationQuery(alter), false)
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare
				if !queued {
					t.Fatal("default did not queue on captured provider")
				}
				cancel()
				lockAwait(t, waiting, context.Canceled)
				waiting = lockAsync(e, b, context.Background(), serialPublicationQuery(alter), false)
				lockPending(t, e, 1)
				transactionEnd(t, a, tx, commit)
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
						sessionSQL(t, b, `CREATE TABLE consumer(id SERIAL)`)
					}
				} else {
					lockAwait(t, waiting, nil)
				}
				sessionSQL(t, b, `INSERT INTO consumer DEFAULT VALUES`)
				sequenceScalar(t, b, `SELECT id FROM consumer`, 1)
				checkReviewDependencyValid(t, e)
				lockEmpty(t, e)
			})
		}
	}
}
