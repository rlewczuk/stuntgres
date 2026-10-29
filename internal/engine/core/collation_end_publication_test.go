package core_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Only the claimed evaluation waits. Reconstructed wrappers share the gate,
// but never wait in sync.Once or while holding the installation mutex.
type endPublicationGate struct {
	engine       *core.Engine
	claimed      atomic.Bool
	entered      chan error
	release      chan struct{}
	unblock      sync.Once
	captured     map[ids.ObjectAddress]*storage.Relation
	entries      map[ids.ObjectAddress]*storage.Relation
	catalog      catalog.Read
	commit       ids.CommitSeq
	row, version uint64
}

func (g *endPublicationGate) open() { g.unblock.Do(func() { close(g.release) }) }

func (g *endPublicationGate) wait(ctx context.Context) error {
	if !g.claimed.CompareAndSwap(false, true) {
		return nil
	}
	if !g.engine.Mu.TryLock() {
		err := fmt.Errorf("index rebuild evaluated under Engine.Mu")
		g.entered <- err
		return err
	}
	g.captured = g.engine.Storage.Relations
	g.entries = maps.Clone(g.captured)
	g.catalog = g.engine.Catalog.Current()
	g.commit = g.engine.Visibility.CommitSeq()
	g.row, g.version = g.engine.Storage.RowFrontier(), g.engine.Storage.VersionFrontier()
	g.engine.Mu.Unlock()
	g.entered <- nil
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The child remains a genuine stored key. Dense-slot remapping must traverse
// it when TEMP retirement removes the middle generated column.
type endPublicationExpression struct {
	expr.Info
	child expr.Expr
	gate  *endPublicationGate
}

func (x *endPublicationExpression) Children() []expr.Expr { return []expr.Expr{x.child} }
func (x *endPublicationExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return &endPublicationExpression{Info: info, child: children[0], gate: x.gate}, nil
}
func (x *endPublicationExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if err := x.gate.wait(ctx.FunctionContext()); err != nil {
		return sqltype.Value{}, err
	}
	return x.child.Eval(ctx)
}

func TestClientEncodingTemporaryEndMaintenancePublication(t *testing.T) {
	// Callable recipes are prepared from Definition, not retained Execution.
	// Use a genuine callable-free key so the Execution wrapper survives recipe
	// preparation while its z child still requires dense-slot remapping.
	e, owner, provider, survivor, idx := temporaryProjectionFixtureWithIndexExpression(t, "CASE WHEN z IS NULL THEN 0 ELSE z END")
	if idx.SQLIndex.RequiresInsertionOutput() {
		t.Fatal("gate fixture must retain its callable-free Execution recipe")
	}
	peer := sessionStart(t, e, nil)
	for _, q := range []string{
		`CREATE COLLATION pg_temp.heap_end_coll FROM pg_catalog."C"`,
		`CREATE TEMP TABLE heap_end_owned(id SERIAL,v TEXT)`,
		`CREATE INDEX heap_end_owned_ix ON heap_end_owned(v)`,
		`INSERT INTO heap_end_owned(v) VALUES('retire')`,
	} {
		sessionSQL(t, owner, q)
	}
	for _, q := range []string{
		`CREATE TABLE heap_end_peer(v TEXT)`,
		`INSERT INTO heap_end_peer VALUES('Я')`,
		`CREATE TEMP TABLE heap_end_peer_temp(v TEXT)`,
		`INSERT INTO heap_end_peer_temp VALUES('peer-kept')`,
	} {
		sessionSQL(t, peer, q)
	}
	address := enginetest.RoleTableRef(t, e, "heap_end_peer").Address
	base := e.CatalogRead()
	peerDescriptor := base.MustRelation(address)
	var peerTemp *catalog.Relation
	for _, d := range base.Descriptors() {
		if r, ok := d.(*catalog.Relation); ok {
			if r.SQLIndex != nil && r.SQLIndex.Parent == address {
				t.Fatal("peer must be genuinely unindexed")
			}
			if r.Name() == "heap_end_peer_temp" {
				peerTemp = r
			}
		}
	}
	if peerTemp == nil {
		t.Fatal("missing peer TEMP")
	}
	oldPeer := e.Storage.Relations[address]
	oldPeerRows := oldPeer.Versions
	oldPeerCopy := append([]storage.RowVersion(nil), oldPeerRows...)
	for i := range oldPeerCopy {
		oldPeerCopy[i].Values = append([]sqltype.Value(nil), oldPeerCopy[i].Values...)
	}
	oldShape := e.Storage.Relations[survivor.Address()]
	oldShapeRows := oldShape.Versions
	oldShapeCopy := append([]storage.RowVersion(nil), oldShapeRows...)
	for i := range oldShapeCopy {
		oldShapeCopy[i].Values = append([]sqltype.Value(nil), oldShapeCopy[i].Values...)
	}
	oldIndex := e.Storage.IndexRoots[idx.Address()]
	peerTempStore := e.Storage.Relations[peerTemp.Address()]
	namespace := owner.TemporaryNamespace
	gate := &endPublicationGate{engine: e, entered: make(chan error, 1), release: make(chan struct{})}

	// Copy every nested owner before replacing Execution; Definition and all
	// dependency/usage metadata stay untouched. Restore only if still current.
	instrumented := *idx
	definition := *idx.SQLIndex
	definition.Keys = append([]catalog.IndexKey(nil), definition.Keys...)
	derived := *definition.Keys[0].Derived
	derived.Execution = &endPublicationExpression{Info: expr.InfoOf(derived.Execution), child: derived.Execution, gate: gate}
	definition.Keys[0].Derived = &derived
	instrumented.SQLIndex = &definition
	e.Mu.Lock()
	e.Catalog.State.SetDescriptor(idx.Address(), &instrumented)
	e.Mu.Unlock()
	joined := false
	t.Cleanup(func() {
		gate.open()
		owner.RequestEnd(session.CleanClose)
		// Joining is unconditional: fixture teardown must not race maintenance.
		if !joined {
			if err := owner.WaitEnd(context.Background()); err != nil {
				t.Error(err)
			}
		}
		e.Mu.Lock()
		if e.Catalog.State.Get(idx.Address()) == &instrumented {
			e.Catalog.State.SetDescriptor(idx.Address(), idx)
		}
		e.Mu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner.RequestEnd(session.CleanClose)
	select {
	case err := <-gate.entered:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("physical end did not reach detached index rebuild", ctx.Err())
	}
	if gate.catalog.State() != base.State() || gate.catalog.Epoch() != base.Epoch() || gate.entries[address] != oldPeer {
		t.Fatal("gate did not capture the old published heap map")
	}
	tx := transactionBegin(t, peer)
	sessionSQL(t, peer, `TRUNCATE heap_end_peer`)
	sessionSQL(t, peer, `INSERT INTO heap_end_peer VALUES('фоо')`)
	transactionEnd(t, peer, tx, true)
	e.Mu.Lock()
	current := e.Storage.Relations[address]
	committed := e.Visibility.CommitSeq()
	rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
	unchangedCatalog := e.Catalog.State == gate.catalog.State() && e.Catalog.Current().Epoch() == gate.catalog.Epoch() && e.Catalog.State.Get(address) == peerDescriptor && e.Catalog.State.MustRelation(address).Revision() == peerDescriptor.Revision()
	e.Mu.Unlock()
	if !unchangedCatalog || current == oldPeer || len(current.Versions) != 1 {
		t.Fatal("peer did not replace only its physical heap")
	}
	row := current.Versions[0]
	text, err := row.Values[0].Text()
	if err != nil || text != "фоо" || row.Born != committed || committed <= gate.commit || uint64(row.Row) <= gate.row || uint64(row.Version) <= gate.version || uint64(row.Row) > rowFrontier || uint64(row.Version) > versionFrontier {
		t.Fatal("incoherent committed replacement row/frontiers", row, committed, err)
	}
	gate.open()
	if err := owner.WaitEnd(ctx); err != nil {
		t.Fatal("physical end maintenance", err)
	}
	joined = true

	e.Mu.Lock()
	final := e.Catalog.Current()
	retainedPeer := e.Storage.Relations[address] == current
	frontiers := e.Visibility.CommitSeq() == committed && e.Storage.RowFrontier() == rowFrontier && e.Storage.VersionFrontier() == versionFrontier
	shape := e.Storage.Relations[survivor.Address()]
	shapeDescriptor := final.MustRelation(survivor.Address())
	root := e.Storage.IndexRoots[idx.Address()]
	peerKept := final.Get(peerTemp.Address()) == peerTemp && e.Storage.Relations[peerTemp.Address()] == peerTempStore
	capturedUnchanged := maps.Equal(gate.captured, gate.entries)
	removed := final.Get(provider.Address()) == nil
	for a, d := range base.Descriptors() {
		if d.Namespace() == namespace || d.Name() == "shape_external" {
			removed = removed && final.Get(a) == nil && e.Storage.Relations[a] == nil && e.Storage.IndexRoots[a] == nil && e.Storage.Sequences[a] == nil
		}
	}
	e.Mu.Unlock()
	if !retainedPeer {
		t.Fatal("maintenance reinstalled the retired peer heap instead of committed фоо")
	}
	if !frontiers || !peerKept || !capturedUnchanged || !removed || owner.TemporaryNamespace != (ids.ObjectAddress{}) || final.Get(address) != peerDescriptor {
		t.Fatal("maintenance lost frontiers, borrowed map, peer identity or owned removals")
	}
	if shape == oldShape || len(shape.Versions) != 1 || len(shape.Versions[0].Values) != 2 || len(shapeDescriptor.Columns) != 2 || shapeDescriptor.Columns[1].Name != "z" || len(shapeDescriptor.ColumnMeta.Generations) != 0 || root == nil || root == oldIndex || len(root.Entries) != 1 || len(root.Entries[0].Keys) != 1 || !final.MustRelation(idx.Address()).SQLIndex.Valid {
		t.Fatal("owned projected heap/index replacement was not installed")
	}
	a, ae := shape.Versions[0].Values[0].Integer()
	z, ze := shape.Versions[0].Values[1].Integer()
	key, ke := root.Entries[0].Keys[0].Integer()
	if ae != nil || ze != nil || ke != nil || a != 7 || z != 9 || key != 9 || root.Entries[0].Row != shape.Versions[0].Row || root.Entries[0].Version != shape.Versions[0].Version {
		t.Fatal("real dense-slot remapping/index reconstruction lost (7,9)")
	}
	if !storage.SameVersionRoot(oldPeer.Versions, oldPeerRows) || !storage.SameVersionRoot(oldShape.Versions, oldShapeRows) || !reflect.DeepEqual(oldPeerRows, oldPeerCopy) || !reflect.DeepEqual(oldShapeRows, oldShapeCopy) || len(oldShapeRows[0].Values) != 3 {
		t.Fatal("maintenance mutated retained version vectors")
	}
	oldText, oldErr := oldPeerRows[0].Values[0].Text()
	if oldErr != nil || oldText != "Я" {
		t.Fatal("old peer payload changed", oldText, oldErr)
	}
	oldA, oldAE := oldShapeRows[0].Values[0].Integer()
	oldG, oldGE := oldShapeRows[0].Values[1].Boolean()
	oldZ, oldZE := oldShapeRows[0].Values[2].Integer()
	if oldAE != nil || oldGE != nil || oldZE != nil || oldA != 7 || !oldG || oldZ != 9 {
		t.Fatal("retained survivor payload is not (7,true,9)")
	}
	assertTemporaryNamespacePair(t, e, owner.SessionToken, namespace)
	for _, q := range []struct{ sql, value string }{
		{`SELECT v FROM heap_end_peer`, "фоо"},
		{`SELECT v FROM heap_end_peer_temp`, "peer-kept"},
	} {
		if got := enginetest.Tuples(sessionSQL(t, peer, q.sql)); !reflect.DeepEqual(got, [][]driver.Value{{q.value}}) {
			t.Fatal(q.sql, got)
		}
	}
	enginetest.LockEmpty(t, e)
}
