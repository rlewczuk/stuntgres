package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"sync"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func collationPrivateSession(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s, err := core.EngineStartSession(e, context.Background(), session.SessionStart{Authenticated: e.RoleStore.Domain.Principal(10)}, true)
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
	e := engine.New()
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
	_, err = e.Execute(op, enginetest.Statement(t, `CREATE COLLATION pg_temp.private_recursive FROM pg_catalog."C"`), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "private maintenance cannot create a temporary namespace") || e.CatalogRead().State() != before.State() {
		t.Fatal("private session recursively created TEMP", err)
	}
	enginetest.LockEmpty(t, e)
}

func TestCollationTemporaryCleanupAX(t *testing.T) {
	e := engine.New()
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
	ref := enginetest.CatalogRef(t, e, before, "coll_cleanup_t")
	if err := e.Locks.Acquire(context.Background(), peer.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	owner.RequestEnd(session.CleanClose)
	enginetest.LockPending(t, e, 1)
	lockState := e.Locks.Inspect()
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
			if _, ok := e.CatalogRead().Descriptor(address); ok || e.Storage.Relations[address] != nil || e.Storage.Sequences[address] != nil || e.Storage.IndexRoots[address] != nil {
				t.Fatal("owned TEMP child or physical projection survived", address)
			}
		}
	}
	r := sessionSQL(t, peer, `SELECT * FROM coll_cleanup_t`)
	if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) || len(r.Rows.Schema()) != 1 {
		t.Fatal("cleanup lost stable surviving storage", got)
	}
	enginetest.LockEmpty(t, e)
}

func TestCollationTemporaryCleanupCompletenessFence(t *testing.T) {
	e := engine.New()
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
	if _, err := core.EngineRemoveTemporaryNamespaceCandidate(e, private, base, []*catalog.Namespace{ns}, children, owner.SessionToken); err == nil {
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
	enginetest.LockEmpty(t, e)
}

func TestCollationTemporaryCleanupExhaustion(t *testing.T) {
	e := engine.New()
	owner, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, `CREATE COLLATION pg_temp.coll_cleanup_exhausted FROM pg_catalog."C"`)
	sessionSQL(t, owner, `CREATE TABLE coll_cleanup_exhausted_t(id INT,v TEXT COLLATE pg_temp.coll_cleanup_exhausted)`)
	sessionSQL(t, owner, `INSERT INTO coll_cleanup_exhausted_t VALUES(7,'é')`)
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	state.SetNamespaceGeneration(^uint64(0))
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
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
		if descriptor.Namespace() == namespace && (after.Get(address) != nil || e.Storage.Relations[address] != nil || e.Storage.Sequences[address] != nil || e.Storage.IndexRoots[address] != nil) {
			t.Fatal("exhausted cleanup retained TEMP child", address)
		}
	}
	if _, ok := before.Descriptor(namespace); !ok {
		t.Fatal("cleanup mutated captured namespace root")
	}
	result := sessionSQL(t, sessionStart(t, e, nil), "SELECT * FROM coll_cleanup_exhausted_t")
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) || len(result.Rows.Schema()) != 1 {
		t.Fatal("exhausted cleanup lost projected surviving rows", got)
	}
	enginetest.LockEmpty(t, e)
}

// The context is installed only after validation. The genuine index rebuild's
// first detached cancellation check is after the heap-map candidate is copied.
// Locked admission/final checks cannot stop here or run a peer under Engine.Mu.
type temporaryPublicationContext struct {
	context.Context
	engine *core.Engine
	once sync.Once
	entered chan struct{}
	release chan struct{}
}

func (c *temporaryPublicationContext) Err() error {
	if err := c.Context.Err(); err != nil { return err }
	if c.engine.Mu.TryLock() {
		c.engine.Mu.Unlock()
		c.once.Do(func() {
			close(c.entered)
			select {
			case <-c.release:
			case <-c.Context.Done():
			}
		})
	}
	return c.Context.Err()
}

func TestClientEncodingTemporaryPublication(t *testing.T) {
	// A real TEMP provider's retirement removes a generated survivor column.
	// Its real remaining expression index supplies the detached-build boundary;
	// the independently committed encoded-text table has no index/dependency.
	e, _, provider, _, _ := temporaryProjectionFixture(t)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE ce_cleanup_storage(v TEXT)")
	sessionSQL(t, peer, "INSERT INTO ce_cleanup_storage VALUES('象')")
	address := enginetest.RoleTableRef(t, e, "ce_cleanup_storage").Address
	base := e.CatalogRead()
	oldStore := e.Storage.Relations[address]
	stage := e.BeginCatalogStage(base)
	core.SetStageTerminalCleanup(stage, true)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), provider.Address())); err != nil { t.Fatal(err) }
	if err := core.StageExpressionDependencies(t.Context(), stage, []ids.ObjectAddress{provider.Address()}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil { t.Fatal(err) }
	prepared, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	barrier := &temporaryPublicationContext{Context: ctx, engine: e, entered: make(chan struct{}), release: make(chan struct{})}
	core.SetPreparedCatalogPublicationCtx(prepared, barrier)
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		select { case <-barrier.release: default: close(barrier.release) }
		if !joined {
			select { case <-done: case <-time.After(5*time.Second): t.Error("maintenance publication did not stop") }
		}
	}()
	go func() {
		_, err := e.PublishPreparedCatalog(nil, nil, prepared, core.PublicationToken{Epoch: base.Epoch()})
		done <- err
	}()
	select {
	case <-barrier.entered:
	case err := <-done:
		joined = true
		t.Fatal("maintenance did not reach detached index rebuild", err)
	case <-ctx.Done():
		t.Fatal("maintenance did not reach detached index rebuild", ctx.Err())
	}
	sessionSQL(t, peer, "TRUNCATE ce_cleanup_storage")
	sessionSQL(t, peer, "INSERT INTO ce_cleanup_storage VALUES('あ')")
	current := e.Storage.Relations[address]
	if current == oldStore || e.CatalogRead().State() != base.State() || len(current.Versions) != 1 || len(oldStore.Versions) != 1 {
		t.Fatal("peer did not replace the actual heap without catalog replacement")
	}
	newText, newErr := current.Versions[0].Values[0].Text()
	oldText, oldErr := oldStore.Versions[0].Values[0].Text()
	if newErr != nil || oldErr != nil || newText != "あ" || oldText != "象" {
		t.Fatal("missing distinguishing committed heap backing", newText, oldText, newErr, oldErr)
	}
	close(barrier.release)
	select {
	case err := <-done:
		joined = true
		if err != nil { t.Fatal("maintenance publication failed", err) }
	case <-ctx.Done():
		t.Fatal("maintenance publication did not finish", ctx.Err())
	}
	if e.Storage.Relations[address] != current {
		t.Fatal("maintenance reinstalled the retired encoded-text heap")
	}
	result := sessionSQL(t, peer, "SELECT v FROM ce_cleanup_storage")
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{"あ"}}) {
		t.Fatal("maintenance lost the successful peer write", got)
	}
}
