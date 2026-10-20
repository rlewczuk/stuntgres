package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestCheckReviewDependencyCreationGrant(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE SEQUENCE s")
				ref := catalogRef(t, e, e.CatalogRead(), "s")
				tx := transactionBegin(t, a)
				sessionSQL(t, a, "DROP SEQUENCE s CASCADE")
				const create = "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"
				if prepared {
					lockAwait(t, lockAsync(e, b, context.Background(), create, true), nil)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waiting := lockAsync(e, b, ctx, create, false)
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[a.Transactions.Ticket].Mode&lock.AccessExclusive != 0
				if !queued {
					t.Fatal("CREATE did not queue AS on exact provider")
				}
				cancel()
				lockAwait(t, waiting, context.Canceled)
				waiting = lockAsync(e, b, context.Background(), create, false)
				lockPending(t, e, 1)
				transactionEnd(t, a, tx, commit)
				if commit {
					select {
					case err := <-waiting:
						if err == nil {
							t.Fatal("deleted identity admitted")
						}
					case <-time.After(5 * time.Second):
						t.Fatal("CREATE did not resume")
					}
					sessionSQL(t, b, "CREATE TABLE newly_dependent(a INT CHECK(a=1))")
				} else {
					lockAwait(t, waiting, nil)
				}
				sessionSQL(t, b, "INSERT INTO newly_dependent VALUES(1)")
				checkReviewDependencyValid(t, e)
				lockEmpty(t, e)
			})
		}
	}
}

func TestCheckReviewDependencyActualCreatePublicationHold(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s")
	ref := catalogRef(t, e, e.CatalogRead(), "s")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var gated atomic.Bool
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{e.catalog.Providers[ids.RelationClassID], func() {
		if gated.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}}
	creating := lockAsync(e, a, context.Background(), "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))", false)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("CREATE validation seam missing")
	}
	dropping := lockAsync(e, b, context.Background(), "DROP SEQUENCE s CASCADE", false)
	lockPending(t, e, 1)
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	held := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessExclusive
	if held {
		held = false
		for _, mode := range entry.Holders {
			held = held || mode.Mode&lock.AccessShare != 0
		}
	}
	if !held {
		t.Fatal("CREATE did not retain provider AS through validation/publication")
	}
	once.Do(func() { close(release) })
	lockAwait(t, creating, nil)
	lockAwait(t, dropping, nil)
	sessionSQL(t, a, "INSERT INTO newly_dependent VALUES(1)")
	checkReviewDependencyValid(t, e)
	read := e.CatalogRead()
	d := read.MustRelation(catalogRef(t, e, read, "newly_dependent").Address)
	if len(d.Constraints.Check) != 0 {
		t.Fatal("grant-time CASCADE missed published CHECK")
	}
	lockEmpty(t, e)
}

func TestCheckReviewDependencyOrderRoundtrip(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s")
	sessionSQL(t, a, "CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))")
	var dump strings.Builder
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	d := e.CatalogRead().MustRelation(catalogRef(t, e, e.CatalogRead(), "t").Address)
	if d.Constraints.Check[0].Name != "c1" || orderedChecks(d)[0].Name != "c2" {
		t.Fatal("enforcement/declaration order conflated")
	}
}
