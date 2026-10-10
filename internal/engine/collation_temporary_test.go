package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"
)

func collationPrivateSession(t *testing.T, e *Engine) *Session {
	t.Helper()
	s, err := e.startSession(context.Background(), SessionStart{Authenticated: PrincipalRef{e, 10}}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestCollationPrivateSessionIdentity(t *testing.T) {
	e := New()
	a, b := collationPrivateSession(t, e), collationPrivateSession(t, e)
	if a.token.id != 0 || b.token.id != 0 || a.token.private != a || b.token.private != b || a.token == b.token {
		t.Fatal("private sessions alias numeric identity", a.token, b.token)
	}
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	roleIdentity(t, e, op, "postgres")
	before := e.CatalogRead()
	_, err = e.Execute(op, statement(t, `CREATE COLLATION pg_temp.private_recursive FROM pg_catalog."C"`), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "private maintenance cannot create a temporary namespace") || e.CatalogRead().state != before.state {
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
	namespace := owner.temporaryNamespace
	before := e.CatalogRead()
	tx := transactionBegin(t, peer)
	ref := catalogRef(t, e, before, "coll_cleanup_t")
	if err := e.locks.acquire(context.Background(), peer.transactions.ticket, lockResource{ObjectRef: ref}, accessShare); err != nil {
		t.Fatal(err)
	}
	owner.RequestEnd(CleanClose)
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ref}]
	valid := entry != nil && len(entry.pending) == 1 && entry.pending[0].mode == accessExclusive && entry.pending[0].ticket.ref.session.private != nil
	e.locks.mu.Unlock()
	if !valid || e.CatalogRead().state != before.state {
		t.Fatal("cleanup bypassed genuine AX or published before lock admission")
	}
	transactionEnd(t, peer, tx, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.CatalogRead().Descriptor(namespace); ok {
		t.Fatal("namespace survived successful atomic cleanup")
	}
	for address, descriptor := range before.state.descriptors {
		if descriptor.Namespace() == namespace {
			if _, ok := e.CatalogRead().Descriptor(address); ok || e.storage[address] != nil || e.sequences[address] != nil || e.indexRoots[address] != nil {
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
	ns := base.state.descriptors[owner.temporaryNamespace].(*namespaceDescriptor)
	var children []ObjectAddress
	for a, d := range base.state.descriptors {
		if d.Namespace() == ns.address {
			children = append(children, a)
		}
	}
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup_late FROM pg_catalog."POSIX"`)
	current := e.CatalogRead()
	private := collationPrivateSession(t, e)
	if err := e.removeTemporaryNamespaceCandidate(private, base, []*namespaceDescriptor{ns}, children, owner.token); err == nil {
		t.Fatal("late owned child was blind-deleted")
	}
	if e.CatalogRead().state != current.state {
		t.Fatal("stale candidate partially published namespace or children")
	}
	for _, name := range []string{"coll_cleanup_first", "coll_cleanup_late"} {
		if len(current.Lookup(NameKey{ClassID: CollationClassID, Namespace: ns.address, Name: name})) != 1 {
			t.Fatal("cleanup lost owned child", name)
		}
	}
	lockEmpty(t, e)
}

func TestCollationTemporaryCleanupExhaustion(t *testing.T) {
	e := New()
	owner, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup_exhausted FROM pg_catalog."C"`)
	sessionSQL(t, owner, `CREATE TABLE coll_cleanup_exhausted_t(id INT,v TEXT COLLATE pg_temp.coll_cleanup_exhausted)`)
	sessionSQL(t, owner, `INSERT INTO coll_cleanup_exhausted_t VALUES(7,'é')`)
	e.mu.Lock()
	state := cloneCatalog(e.state)
	state.namespaceGeneration = ^uint64(0)
	e.state = state
	e.mu.Unlock()
	before := e.CatalogRead()
	namespace := owner.temporaryNamespace
	owner.RequestEnd(CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = owner.WaitEnd(ctx)
	if err == nil || !strings.Contains(err.Error(), "namespace invalidation generation exhausted") {
		t.Fatal("cleanup suppressed actual publication exhaustion", err)
	}
	if e.CatalogRead().state != before.state {
		t.Fatal("failed cascade fell back to generic namespace deletion")
	}
	if _, ok := before.Descriptor(namespace); !ok || owner.temporaryNamespace != namespace {
		t.Fatal("failure discarded exact owned namespace bookkeeping")
	}
	lockEmpty(t, e)
}
