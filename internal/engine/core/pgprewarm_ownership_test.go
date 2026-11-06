package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func prewarmOwnerEngine(t *testing.T) (*core.Engine, ids.ObjectRef, plan.InstalledPgPrewarmExpr) {
	t.Helper()
	e := bufferUnitEngine(t)
	for _, q := range []string{"CREATE EXTENSION pg_prewarm", "CREATE TABLE prewarm_heap(k integer)", "INSERT INTO prewarm_heap SELECT generate_series(1,1000)"} { enginetest.Run(t, e, q) }
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	node, err := binder.BindFunctionSource(enginetest.FunctionCall("pg_prewarm", enginetest.StringLiteral("-")))
	if err != nil { t.Fatal(err) }
	return e, enginetest.CatalogRef(t, e, e.CatalogRead(), "prewarm_heap"), node.(plan.InstalledPgPrewarmExpr)
}
func prewarmOwnerScope(t *testing.T, ctx context.Context, e *core.Engine, actor *session.Session) (*core.StatementScope, func(error) error) {
	t.Helper()
	op, err := actor.Acquire(ctx)
	if err != nil { t.Fatal(err) }
	scope, err := e.StartStatement(op)
	if err != nil { op.Done(); t.Fatal(err) }
	finished := false
	finish := func(failure error) error { if finished { return failure }; finished = true; err := scope.Finish(failure); op.Done(); return err }
	t.Cleanup(func() { finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if result.Rows != nil { result.Rows.Close() }
	if err != nil { finish(err); t.Fatal(err) }
	return scope, finish
}
func prewarmOwnerCoordinates(t *testing.T, e *core.Engine, actor *session.Session, node ids.OID) map[[2]int64][2]int64 {
	t.Helper()
	cursor := bufferUnitCursor(t, e, actor)
	defer cursor.Close()
	result := map[[2]int64][2]int64{}
	for {
		row, present, err := cursor.Next(t.Context())
		if err != nil { t.Fatal(err) }
		if !present { break }
		if row.CompositeField(1).IsNull() { continue }
		oid, err := row.CompositeField(1).OID()
		if err != nil { t.Fatal(err) }
		if ids.OID(oid) != node { continue }
		fork, err := row.CompositeField(4).Integer(); if err != nil { t.Fatal(err) }
		block, err := row.CompositeField(5).Integer(); if err != nil { t.Fatal(err) }
		usage, err := row.CompositeField(7).Integer(); if err != nil { t.Fatal(err) }
		pins, err := row.CompositeField(8).Integer(); if err != nil { t.Fatal(err) }
		key := [2]int64{fork, block}
		if _, duplicate := result[key]; duplicate { t.Fatal("duplicate coordinate", key) }
		result[key] = [2]int64{usage, pins}
	}
	return result
}

type prewarmCountingHost struct {
	contrib.Host[core.AuthorizationView]
	completed int
	cancelAfter int
	cancel context.CancelFunc
}
func (h *prewarmCountingHost) PrewarmBlockLocked(ref ids.ObjectRef, fork int16, block int64) error {
	if err := h.Host.PrewarmBlockLocked(ref, fork, block); err != nil { return err }
	h.completed++
	if h.cancel != nil && h.completed == h.cancelAfter { h.cancel() }
	return nil
}

type prewarmReleaseHost struct {
	*prewarmCountingHost
	releases int
	afterCapture func()
}
func (h *prewarmReleaseHost) ReleaseOwnerEngineLocked() {
	h.Host.ReleaseOwnerEngineLocked()
	h.releases++
	if h.releases == 1 && h.afterCapture != nil { h.afterCapture() }
}

func TestPgPrewarmCapture(t *testing.T) {
	e, ref, n := prewarmOwnerEngine(t)
	actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
	host := &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView])}
	store := e.Storage.Relations[ref.Address]
	rows, ends, generation := store.Versions, store.Ends, store.Generation
	count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork: 0, First: 0, Last: 3})
	if err != nil || count != 4 || host.completed != 4 { t.Fatal("real range processing", count, host.completed, err) }
	if err := finish(nil); err != nil { t.Fatal(err) }
	if !storage.SameVersionRoot(rows, store.Versions) || ends != store.Ends || generation != store.Generation { t.Fatal("preload changed captured heap roots") }
	want := map[[2]int64][2]int64{{0,0}: {1,0}, {0,1}: {1,0}, {0,2}: {1,0}, {0,3}: {1,0}}
	if got := prewarmOwnerCoordinates(t, e, observer, bufferUnitNode(t, e, "prewarm_heap")); !reflect.DeepEqual(got, want) { t.Fatal("count-only or wrong-coordinate preload", got, want) }
	// Publish a genuine descriptor following the existing freespace owner
	// pattern: native's public btree hierarchy constructor is unavailable.
	enginetest.Run(t, e, "CREATE ROLE prewarm_kind_reader")
	enginetest.Run(t, e, "CREATE TABLE prewarm_parent(k integer) PARTITION BY RANGE(k)")
	enginetest.Run(t, e, "CREATE INDEX prewarm_parent_idx ON prewarm_heap(k)")
	view := e.CatalogRead()
	parent := enginetest.CatalogRef(t, e, view, "prewarm_parent")
	indexRef := enginetest.CatalogRef(t, e, view, "prewarm_parent_idx")
	target := *view.MustRelation(indexRef.Address)
	definition := *target.SQLIndex
	definition.Parent = parent.Address
	target.SQLIndex = &definition
	target.ObjectRevision = target.Revision()+1
	before := e.Catalog.State
	t.Cleanup(func() { e.Mu.Lock(); e.Catalog.PublishLocked(before); e.Mu.Unlock() })
	state := catalog.CloneState(before)
	state.SetDescriptor(indexRef.Address, &target)
	e.Mu.Lock()
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
	request := storage.PrewarmRequest{Fork: 3, First: -1, Last: -1}
	for _, current := range []struct{ role string; want sqlstate.Error }{
		{"postgres", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "relation \"prewarm_parent_idx\" does not have storage", Detail: "This operation is not supported for partitioned indexes."}},
		{"prewarm_kind_reader", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for index prewarm_parent_idx"}},
	} {
		enginetest.SessionSQL(t, actor, "SET SESSION AUTHORIZATION "+current.role)
		scope, finish = prewarmOwnerScope(t, t.Context(), e, actor)
		count, err = contrib.PgPrewarm[core.AuthorizationView](core.HostOf(scope).(contrib.Host[core.AuthorizationView]), n, uint32(indexRef.Address.ObjectID), request)
		var diagnostic *sqlstate.Error
		if count != 0 || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, current.want) { t.Fatal("genuine descriptor ACL/kind/fork/range precedence", current.role, diagnostic, err) }
		finish(err)
	}
}

func TestPgPrewarmOwnership(t *testing.T) {
	t.Run("unlocked_capture_then_currentness", func(t *testing.T) {
		e, ref, n := prewarmOwnerEngine(t)
		actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
		counting := &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView])}
		called := false
		host := &prewarmReleaseHost{prewarmCountingHost: counting, afterCapture: func() {
			called = true
			if !scope.Owner.Mu.TryLock() { t.Fatal("capture retained owner lock") }; scope.Owner.Mu.Unlock()
			if !e.Mu.TryLock() { t.Fatal("capture retained Engine lock") }; e.Mu.Unlock()
			// Genuine SQL publication changes the captured descriptor without
			// replacing its heap or requiring an exclusive relation lock.
			enginetest.Run(t, e, "GRANT SELECT ON prewarm_heap TO PUBLIC")
		}}
		count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork: 0, First: 0, Last: 3})
		if !called || !errors.Is(err, ids.ErrCatalogReplaced) || count != 0 || counting.completed != 0 { t.Fatal("stale unlocked capture reached admission", called, count, counting.completed, err) }
		finish(err)
		if got := prewarmOwnerCoordinates(t, e, observer, bufferUnitNode(t, e, "prewarm_heap")); len(got) != 0 { t.Fatal("stale capture populated cache", got) }
	})
	t.Run("canceled_actual_prefix", func(t *testing.T) {
		e, ref, n := prewarmOwnerEngine(t)
		actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		scope, finish := prewarmOwnerScope(t, ctx, e, actor)
		host := &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView]), cancel: cancel, cancelAfter: 2}
		count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork: 0, First: 0, Last: 3})
		if !errors.Is(err, context.Canceled) || count != 0 || host.completed != 2 { t.Fatal("canceled prefix fabricated success", count, host.completed, err) }
		finish(err)
		want := map[[2]int64][2]int64{{0,0}: {1,0}, {0,1}: {1,0}}
		if got := prewarmOwnerCoordinates(t, e, observer, bufferUnitNode(t, e, "prewarm_heap")); !reflect.DeepEqual(got, want) { t.Fatal("completed prefix or released leases", got, want) }
	})
	t.Run("private_incarnation_not_shared", func(t *testing.T) {
		e, _, n := prewarmOwnerEngine(t)
		actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		for _, q := range []string{"BEGIN", "CREATE TABLE prewarm_private(k integer)", "INSERT INTO prewarm_private VALUES(1)"} { enginetest.SessionSQL(t, actor, q) }
		scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
		host := core.HostOf(scope).(contrib.Host[core.AuthorizationView])
		host.ReacquireOwnerEngine()
		view := host.PrivateCatalogLocked(scope.PendingCatalog())
		private := view.Owner().Domain.Ref(view.Epoch(), view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "prewarm_private"}))
		target := view.MustRelation(private.Address)
		node := target.EffectiveStorageLocator(view)
		host.ReleaseOwnerEngineLocked()
		count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(private.Address.ObjectID), storage.PrewarmRequest{DefaultFirst: true, DefaultLast: true})
		if err != nil || count != 1 { t.Fatal("private geometry", count, err) }
		finish(err)
		if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("private preload escaped into cluster", got) }
		enginetest.SessionSQL(t, actor, "ROLLBACK")
	})
}

func TestPgPrewarmPublication(t *testing.T) {
	e, ref, n := prewarmOwnerEngine(t)
	enginetest.Run(t, e, "VACUUM (FREEZE) prewarm_heap")
	actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
	count, err := contrib.PgPrewarm[core.AuthorizationView](core.HostOf(scope).(contrib.Host[core.AuthorizationView]), n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork: 0, First: 1, Last: 2})
	if err != nil || count != 2 { t.Fatal(count, err) }
	finish(err)
	for _, fork := range []int16{1, 2} {
		scope, finish = prewarmOwnerScope(t, t.Context(), e, actor)
		count, err = contrib.PgPrewarm[core.AuthorizationView](core.HostOf(scope).(contrib.Host[core.AuthorizationView]), n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork: fork, First: 0, Last: 0})
		if err != nil || count != 1 { t.Fatal("actual maintained auxiliary preload", fork, count, err) }
		if err := finish(nil); err != nil { t.Fatal(err) }
	}
	node := bufferUnitNode(t, e, "prewarm_heap")
	before := prewarmOwnerCoordinates(t, e, observer, node)
	if len(before) != 4 || before[[2]int64{1,0}] != ([2]int64{1,0}) || before[[2]int64{2,0}] != ([2]int64{1,0}) { t.Fatal("publication fixture lacks genuine auxiliary/nonzero occupancy", before) }
	base := e.CatalogRead()
	bad := catalog.CloneState(base.State())
	bad.RemoveDescriptor(n.Procedure.Address())
	if _, err := e.ReplaceCatalog(t.Context(), e, e.Catalog.Read(bad)); err == nil { t.Fatal("invalid provider replacement accepted") }
	if e.CatalogRead().State() != base.State() || !reflect.DeepEqual(before, prewarmOwnerCoordinates(t, e, observer, node)) { t.Fatal("failed publication mutated incumbent cache") }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.ReplaceCatalog(ctx, e, base); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	if !reflect.DeepEqual(before, prewarmOwnerCoordinates(t, e, observer, node)) { t.Fatal("canceled publication retired coordinates") }
	for _, ddl := range []string{"ALTER TABLE prewarm_heap RENAME TO prewarm_kept", "GRANT SELECT ON prewarm_kept TO PUBLIC", "REVOKE SELECT ON prewarm_kept FROM PUBLIC"} {
		enginetest.Run(t, e, ddl)
		if !reflect.DeepEqual(before, prewarmOwnerCoordinates(t, e, observer, node)) { t.Fatal("presentation/ACL retired nonzero coordinates", ddl) }
	}
	if _, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil { t.Fatal(err) }
	if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("replacement retained old coordinates", got) }
}
