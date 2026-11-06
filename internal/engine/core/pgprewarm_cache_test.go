package core

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// This owner test exercises genuine statement-host admissions at capacity two.
// The composed contrib test separately proves whole-range processed counts and
// capture/range/cancellation authority; a test loop is not a function result.
func TestPgPrewarmCache(t *testing.T) {
	e, err := NewEngine(nil, nil, "prewarm_cache_owner", Startup{})
	if err != nil { t.Fatal(err) }
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	relation := &catalog.Relation{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "prewarm_cache_owner", ObjectRevision: 2, RelationKind: catalog.TableRelation, Owner: e.RoleStore.Domain.Principal(10)}
	state.SetDescriptor(address, relation)
	state.BindName(catalog.DescriptorNameKey(relation), address)
	e.Storage.InstallRelationLocked(address, &storage.Relation{})
	e.Catalog.PublishLocked(state)
	originalCache := e.Cluster.buffercache
	cache := &logicalBufferCache{capacity: 2}
	e.Cluster.buffercache = cache
	e.Mu.Unlock()
	defer func() { e.Mu.Lock(); e.Cluster.buffercache = originalCache; e.Mu.Unlock() }()
	principal, err := e.ResolveLogin("postgres")
	if err != nil { t.Fatal(err) }
	start := session.SessionStart{ID: 1}
	admission, err := e.AdmitDatabase(t.Context(), principal, start.ID, DatabaseSelector{Name: e.DatabaseName, Explicit: true})
	if err != nil { t.Fatal(err) }
	defer admission.Release()
	actor, err := e.StartSession(t.Context(), admission, start)
	if err != nil { t.Fatal(err) }
	defer func() { actor.RequestEnd(session.CleanClose); if err := actor.WaitEnd(context.Background()); err != nil { t.Error(err) } }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	op, err := actor.Acquire(ctx)
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(nil)
	statements, err := parser.Parse("SELECT 1")
	if err != nil { t.Fatal(err) }
	if err := scope.Gate(statements[0]); err != nil { t.Fatal(err) }
	h := (*statementHost)(scope)
	// Borrow the authority created by the real admitted SELECT source, as
	// production read bridges do. Open still applies its live owner gate.
	e.Mu.Lock()
	source, err := e.CaptureQuerySourcesLocked(scope, nil, true)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	defer source.Close()
	if source.Authority == nil || source.Authority.Host != h { t.Fatal("admitted singleton source lost actual statement authority") }
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	admit := func(block int64) error {
		h.ReacquireOwnerEngine()
		err := h.PrewarmBlockLocked(ref, 0, block)
		h.ReleaseOwnerEngineLocked()
		return err
	}
	for block := int64(0); block < 4; block++ { if err := admit(block); err != nil { t.Fatal(err) } }
	_, rows := cache.snapshot()
	if len(rows) != 2 || rows[1].block != 2 || rows[2].block != 3 || rows[1].usage != 1 || rows[2].usage != 1 || rows[1].pins != 0 || rows[2].pins != 0 { t.Fatal("real streamed eviction", rows) }
	for range 7 { if err := admit(3); err != nil { t.Fatal(err) } }
	_, rows = cache.snapshot()
	if rows[2].usage != 5 || rows[2].pins != 0 { t.Fatal("resident count saturates without leaked lease", rows) }
	identity, eligible := logicalBufferIdentity(e.Catalog.Current(), relation)
	if !eligible { t.Fatal("fixture lost genuine eligible incarnation") }
	first := &logicalBufferRead{engine: e, key: logicalBufferKey{logicalBufferIncarnation: identity, block: 2}, owner: actor.SessionToken, authority: source.Authority}
	second := &logicalBufferRead{engine: e, key: logicalBufferKey{logicalBufferIncarnation: identity, block: 3}, owner: actor.SessionToken, authority: source.Authority}
	first.Open()
	second.Open()
	retiredEntry := first.entry
	defer first.Close()
	defer second.Close()
	if err := admit(0); err != nil { t.Fatal(err) }
	_, pinned := cache.snapshot()
	if len(pinned) != 2 || pinned[1].block != 2 || pinned[2].block != 3 || pinned[1].pins != 1 || pinned[2].pins != 1 { t.Fatal("all-pinned admission stole ownership", pinned) }
	first.Close()
	second.Close()
	if err := admit(0); err != nil { t.Fatal(err) }
	_, rows = cache.snapshot()
	if rows[1].block != 0 || rows[1].pins != 0 { t.Fatal("released victim not reusable", rows) }
	// Retain an actual stale entry token across eviction, then pin the genuine
	// replacement. Closing the stale token cannot decrement its session pin.
	replacement := &logicalBufferRead{engine: e, key: logicalBufferKey{logicalBufferIncarnation: identity, block: 0}, owner: actor.SessionToken, authority: source.Authority}
	replacement.Open()
	_, beforeLate := cache.snapshot()
	if beforeLate[1].pins != 1 { t.Fatal("missing real replacement pin", beforeLate) }
	cache.close(retiredEntry, actor.SessionToken)
	if _, afterLate := cache.snapshot(); afterLate[1] != beforeLate[1] { t.Fatal("late close changed replacement", beforeLate, afterLate) }
	replacement.Close()
	_, rows = cache.snapshot()
	cancel()
	if err := admit(1); !errors.Is(err, context.Canceled) { t.Fatal("canceled admitted owner", err) }
	_, after := cache.snapshot()
	if len(after) != len(rows) || after[1] != rows[1] || after[2] != rows[2] { t.Fatal("canceled host changed cache", rows, after) }
}
