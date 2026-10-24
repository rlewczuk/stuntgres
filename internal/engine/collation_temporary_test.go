package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func collationPrivateSession(t *testing.T, e *Engine) *session.Session {
	t.Helper()
	s, err := e.startSession(context.Background(), session.SessionStart{Authenticated: e.roles.Domain.Principal(10)}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestCollationPrivateSessionIdentity(t *testing.T) {
	e := New()
	a, b := collationPrivateSession(t, e), collationPrivateSession(t, e)
	if a.SessionToken.ID() != 0 || b.SessionToken.ID() != 0 || session.PrivateSessionOf(a.SessionToken) != a || session.PrivateSessionOf(b.SessionToken) != b || a.SessionToken == b.SessionToken {
		t.Fatal("private sessions alias numeric identity", a.SessionToken, b.SessionToken)
	}
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	roleIdentity(t, e, op, "postgres")
	before := e.CatalogRead()
	_, err = e.Execute(op, statement(t, `CREATE COLLATION pg_temp.private_recursive FROM pg_catalog."C"`), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "private maintenance cannot create a temporary namespace") || e.CatalogRead().State() != before.State() {
		t.Fatal("private session recursively created TEMP", err)
	}
	lockEmpty(t, e)
}

func TestCollationTemporaryCleanupAX(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup FROM pg_catalog."C"`)
	sessionSQL(t, owner, `CREATE TABLE coll_cleanup_t(id INT,v TEXT COLLATE pg_temp.coll_cleanup)`)
	sessionSQL(t, owner, `INSERT INTO coll_cleanup_t VALUES(7,'é')`)
	sessionSQL(t, owner, `CREATE TEMP TABLE coll_cleanup_local(id SERIAL,v TEXT COLLATE pg_temp.coll_cleanup)`)
	sessionSQL(t, owner, `CREATE INDEX coll_cleanup_local_ix ON coll_cleanup_local(v)`)
	sessionSQL(t, owner, `INSERT INTO coll_cleanup_local(v) VALUES('a')`)
	namespace := owner.TemporaryNamespace
	before := e.CatalogRead()
	tx := transactionBegin(t, peer)
	ref := catalogRef(t, e, before, "coll_cleanup_t")
	if err := e.locks.Acquire(context.Background(), peer.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	owner.RequestEnd(session.CleanClose)
	lockPending(t, e, 1)
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	valid := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessExclusive && session.PrivateSessionOf(entry.Pending[0].Ticket.Ref().Session()) != nil
	if !valid || e.CatalogRead().State() != before.State() {
		t.Fatal("cleanup bypassed genuine AX or published before lock admission")
	}
	transactionEnd(t, peer, tx, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	assertTemporaryNamespacePair(t, e, owner.SessionToken, namespace)
	for address, descriptor := range before.Descriptors() {
		if descriptor.Namespace() == namespace {
			if _, ok := e.CatalogRead().Descriptor(address); ok || e.storage.Relations[address] != nil || e.storage.Sequences[address] != nil || e.storage.IndexRoots[address] != nil {
				t.Fatal("owned TEMP child or physical projection survived", address)
			}
		}
	}
	r := sessionSQL(t, peer, `SELECT * FROM coll_cleanup_t`)
	if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) || len(r.Rows.Schema()) != 1 {
		t.Fatal("cleanup lost stable surviving storage", got)
	}
	lockEmpty(t, e)
}

func TestCollationTemporaryCleanupCompletenessFence(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup_first FROM pg_catalog."C"`)
	base := e.CatalogRead()
	ns := base.MustNamespace(owner.TemporaryNamespace)
	var children []ids.ObjectAddress
	for a, d := range base.Descriptors() {
		if d.Namespace() == ns.Address() {
			children = append(children, a)
		}
	}
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup_late FROM pg_catalog."POSIX"`)
	current := e.CatalogRead()
	private := collationPrivateSession(t, e)
	if _, err := e.removeTemporaryNamespaceCandidate(private, base, []*catalog.Namespace{ns}, children, owner.SessionToken); err == nil {
		t.Fatal("late owned child was blind-deleted")
	}
	if e.CatalogRead().State() != current.State() {
		t.Fatal("stale candidate partially published namespace or children")
	}
	for _, name := range []string{"coll_cleanup_first", "coll_cleanup_late"} {
		if len(current.Lookup(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ns.Address(), Name: name})) != 1 {
			t.Fatal("cleanup lost owned child", name)
		}
	}
	lockEmpty(t, e)
}

func TestCollationTemporaryCleanupExhaustion(t *testing.T) {
	e := New()
	owner, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup_exhausted FROM pg_catalog."C"`)
	sessionSQL(t, owner, `CREATE TABLE coll_cleanup_exhausted_t(id INT,v TEXT COLLATE pg_temp.coll_cleanup_exhausted)`)
	sessionSQL(t, owner, `INSERT INTO coll_cleanup_exhausted_t VALUES(7,'é')`)
	e.mu.Lock()
	state := catalog.CloneState(e.catalog.State)
	state.SetNamespaceGeneration(^uint64(0))
	e.catalog.PublishLocked(state)
	e.mu.Unlock()
	before := e.CatalogRead()
	namespace := owner.TemporaryNamespace
	owner.RequestEnd(session.CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = owner.WaitEnd(ctx)
	if err != nil {
		t.Fatal("child-only cleanup failed at namespace-generation exhaustion", err)
	}
	after := e.CatalogRead()
	if after.State() == before.State() || after.State().NamespaceGeneration() != ^uint64(0) || owner.TemporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("cleanup fabricated namespace event or retained active authority")
	}
	assertTemporaryNamespacePair(t, e, owner.SessionToken, namespace)
	for address, descriptor := range before.Descriptors() {
		if descriptor.Namespace() == namespace && (after.Get(address) != nil || e.storage.Relations[address] != nil || e.storage.Sequences[address] != nil || e.storage.IndexRoots[address] != nil) {
			t.Fatal("exhausted cleanup retained TEMP child", address)
		}
	}
	if _, ok := before.Descriptor(namespace); !ok {
		t.Fatal("cleanup mutated captured namespace root")
	}
	result := sessionSQL(t, sessionStart(t, e, nil), "SELECT * FROM coll_cleanup_exhausted_t")
	if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) || len(result.Rows.Schema()) != 1 {
		t.Fatal("exhausted cleanup lost projected surviving rows", got)
	}
	lockEmpty(t, e)
}
