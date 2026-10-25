package tablecmds_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
)

func TestCheckReviewDependencyCreationGrant(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				e := engine.New()
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
				tx := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "DROP SEQUENCE s CASCADE")
				const create = "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"
				if prepared {
					enginetest.LockAwait(t, enginetest.LockAsync(e, b, context.Background(), create, true), nil)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waiting := enginetest.LockAsync(e, b, ctx, create, false)
				enginetest.LockPending(t, e, 1)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[a.Transactions.Ticket].Mode&lock.AccessExclusive != 0
				if !queued {
					t.Fatal("CREATE did not queue AS on exact provider")
				}
				cancel()
				enginetest.LockAwait(t, waiting, context.Canceled)
				waiting = enginetest.LockAsync(e, b, context.Background(), create, false)
				enginetest.LockPending(t, e, 1)
				enginetest.TransactionEnd(t, a, tx, commit)
				if commit {
					select {
					case err := <-waiting:
						if err == nil {
							t.Fatal("deleted identity admitted")
						}
					case <-time.After(5 * time.Second):
						t.Fatal("CREATE did not resume")
					}
					enginetest.SessionSQL(t, b, "CREATE TABLE newly_dependent(a INT CHECK(a=1))")
				} else {
					enginetest.LockAwait(t, waiting, nil)
				}
				enginetest.SessionSQL(t, b, "INSERT INTO newly_dependent VALUES(1)")
				checkReviewDependencyValid(t, e)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestCheckReviewDependencyActualCreatePublicationHold(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var gated atomic.Bool
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{e.Catalog.Providers[ids.RelationClassID], func() {
		if gated.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}}
	creating := enginetest.LockAsync(e, a, context.Background(), "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))", false)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("CREATE validation seam missing")
	}
	dropping := enginetest.LockAsync(e, b, context.Background(), "DROP SEQUENCE s CASCADE", false)
	enginetest.LockPending(t, e, 1)
	lockState := e.Locks.Inspect()
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
	enginetest.LockAwait(t, creating, nil)
	enginetest.LockAwait(t, dropping, nil)
	enginetest.SessionSQL(t, a, "INSERT INTO newly_dependent VALUES(1)")
	checkReviewDependencyValid(t, e)
	read := e.CatalogRead()
	d := read.MustRelation(enginetest.CatalogRef(t, e, read, "newly_dependent").Address)
	if len(d.Constraints.Check) != 0 {
		t.Fatal("grant-time CASCADE missed published CHECK")
	}
	enginetest.LockEmpty(t, e)
}

func TestCheckReviewDependencyOrderRoundtrip(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))")
	var dump strings.Builder
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	d := e.CatalogRead().MustRelation(enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address)
	if d.Constraints.Check[0].Name != "c1" || table.OrderedChecks(d)[0].Name != "c2" {
		t.Fatal("enforcement/declaration order conflated")
	}
}
