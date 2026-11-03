package functions_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func surgeryKillExpression(t *testing.T, e *core.Engine) plan.InstalledPgsurgeryExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	node, err := binder.BindFunctionProvider(enginetest.FunctionCall("heap_force_kill", enginetest.StringLiteral("-"), enginetest.StringLiteral("{}")))
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgsurgeryExpr)
}

func TestPgsurgeryKillPublication(t *testing.T) {
	e, ref, _, input := surgeryPublicationFixture(t)
	// Partial roots own explicit membership maps; full roots represent their
	// membership only through Entries. Exercise the genuine map-producing lane.
	enginetest.Run(t, e, "CREATE INDEX surgery_kill_idx ON surgery_heap(n) WHERE n>0")
	node := surgeryKillExpression(t, e)
	store := e.Storage.Relations[ref.Address]
	indexRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "surgery_kill_idx")
	root := e.Storage.IndexRoots[indexRef.Address]
	if root == nil || len(root.Entries) != 1 || root.MembershipLen() != 1 || !root.IsMember(store.Versions[0].Row, store.Versions[0].Version) {
		t.Fatal("fixture lacks a genuine populated partial-index root")
	}
	entries := root.Entries
	entry := root.Entries[0]
	key := entry.Keys[0]
	before, generation := store.Versions[0], store.Generation
	old := *store
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	if err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input); err != nil {
		t.Fatal(err)
	}
	published := store.Headers
	if store.Generation != generation+1 || !published.Apply(before).HeapKilled || old.Headers != nil || old.Headers.Apply(before).HeapKilled || store.Versions[0].HeapKilled || &store.Versions[0].Values[0] != &before.Values[0] {
		t.Fatal("kill mutated captured data instead of immutable exact headers")
	}
	if e.Storage.IndexRoots[indexRef.Address] != root || &root.Entries[0] != &entries[0] || root.Entries[0].Version != entry.Version || root.MembershipLen() != 1 || !root.IsMember(before.Row, before.Version) || root.Entries[0].Row != before.Row || &root.Entries[0].Keys[0] != &entry.Keys[0] || !sqltype.SameDatum(root.Entries[0].Keys[0], key) {
		t.Fatal("kill changed genuine index root, key or membership")
	}
	if rows := storage.VisibleHeapVersions(store, e.Visibility.CommitSeq()); len(rows) != 0 {
		t.Fatal("control capture exported killed data", rows)
	}
	if rows := storage.VisibleHeapVersions(&old, e.Visibility.CommitSeq()); len(rows) != 1 {
		t.Fatal("old control capture mutated", rows)
	}
	if err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input); err != nil || store.Headers != published || store.Generation != generation+1 {
		t.Fatal("repeated dead selection published a new root", err)
	}
	if err := finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if store.Headers != published || !store.Headers.Apply(before).HeapKilled {
		t.Fatal("owner disposal undid death")
	}
}

func TestPgsurgeryKillPrepublication(t *testing.T) {
	e, ref, _, input := surgeryPublicationFixture(t)
	node := surgeryKillExpression(t, e)
	store := e.Storage.Relations[ref.Address]
	store.Generation = math.MaxUint64
	root := store.Headers
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input)
	if err == nil || err.Error() != "stuntgres: heap generation exhausted" || store.Generation != math.MaxUint64 || store.Headers != root {
		t.Fatal("exhaustion changed group", err)
	}
	_ = finish(err)
}

func TestPgsurgeryKillCanceledGroup(t *testing.T) {
	e, ref, _, input := surgeryPublicationFixture(t)
	node := surgeryKillExpression(t, e)
	store := e.Storage.Relations[ref.Address]
	generation, root := store.Generation, store.Headers
	observer := enginetest.StartSession(t, e, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	op, err := observer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	err = core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input)
	if !errors.Is(err, context.Canceled) || store.Generation != generation || store.Headers != root {
		t.Fatal("canceled group published death", err)
	}
}

func surgeryKillInput(t *testing.T, e *core.Engine, row storage.RowVersion) sqltype.Value {
	t.Helper()
	array, _ := e.CatalogRead().LookupType(sqltype.ID(1010))
	block, offset, err := storage.LogicalTID(row.Version)
	if err != nil {
		t.Fatal(err)
	}
	tid, err := sqltype.TIDValue(array.Element(), block, offset)
	if err != nil {
		t.Fatal(err)
	}
	input, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{tid})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestPgsurgeryKillOwnerPublication(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, head := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t_commit_%t_head_%t", prepared, commit, head), func(t *testing.T) {
					e, ref, _, _ := surgeryPublicationFixture(t)
					node := surgeryKillExpression(t, e)
					store := e.Storage.Relations[ref.Address]
					writer, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
					for _, q := range []string{"BEGIN", "UPDATE surgery_heap SET n=2", "UPDATE surgery_heap SET n=3"} {
						enginetest.SessionSQL(t, writer, q)
					}
					pending := writer.Transactions.Writes[ref.Address][0]
					history := pending.PrivateHistory
					target := history.Version
					if head {
						target = pending
					}
					before := *store
					detached := false
					var finishScope func(error) error
					if prepared {
						enginetest.SessionSQL(t, writer, "PREPARE TRANSACTION 'kill_owner'")
						detached = true
						if writer.Transactions.Writes != nil {
							t.Fatal("prepared origin retained writes")
						}
						defer func() {
							if finishScope != nil {
								_ = finishScope(nil)
							}
							if detached {
								enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'kill_owner'")
							}
						}()
					}
					scope, finish := visibilityScope(t, e, observer)
					finishScope = finish
					facts, _ := visibilityFactsAt(t, scope, ref)
					if err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), surgeryKillInput(t, e, target)); err != nil {
						t.Fatal(err)
					}
					capture := store.Headers
					if err := finish(nil); err != nil {
						t.Fatal(err)
					}
					if prepared {
						command := "ROLLBACK PREPARED 'kill_owner'"
						if commit {
							command = "COMMIT PREPARED 'kill_owner'"
						}
						enginetest.SessionSQL(t, observer, command)
						detached = false
					} else {
						enginetest.TransactionEnd(t, writer, writer.Transactions.Ref, commit)
					}
					physical, _, found := store.PhysicalVersion(target.Version, -1)
					if !found || !physical.HeapKilled || physical.Version != target.Version || &physical.Values[0] != &target.Values[0] || before.Headers.Apply(target).HeapKilled || pending.PrivateHistory != history || pending.HeapKilled || !capture.Apply(target).HeapKilled {
						t.Fatal("owner completion resurrected target or changed retained backing")
					}
					for _, fact := range facts {
						if fact.Version.HeapKilled {
							t.Fatal("publication mutated old owner fact capture")
						}
					}
					visible := storage.VisibleHeapVersions(store, e.Visibility.CommitSeq())
					want := 1
					if commit && head {
						want = 0
					}
					if len(visible) != want {
						t.Fatal("visible-data export leaked killed/private version", visible)
					}
					if commit && !head && visible[0].Version != pending.Version {
						t.Fatal("kill retargeted valid distinct successor")
					}
				})
			}
		}
	}
}

func TestPgsurgeryKillIncarnation(t *testing.T) {
	e, ref, _, input := surgeryPublicationFixture(t)
	node := surgeryKillExpression(t, e)
	original := e.Storage.Relations[ref.Address]
	row := original.Versions[0]
	before := *original
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	if err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input); err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	headers := original.Headers
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE EXTENSION pg_surgery", "CREATE TABLE surgery_heap(n integer)", "INSERT INTO surgery_heap VALUES(2)"} {
		enginetest.Run(t, e, q)
	}
	freshRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "surgery_heap")
	fresh := e.Storage.Relations[freshRef.Address]
	if freshRef.Epoch == ref.Epoch || fresh == original || fresh.Headers != nil || !headers.Apply(row).HeapKilled || before.Headers.Apply(row).HeapKilled || &before.Versions[0].Values[0] != &row.Values[0] {
		t.Fatal("replacement borrowed stale storage/header authority")
	}
	generation := fresh.Generation
	newObserver := enginetest.StartSession(t, e, nil)
	scope, finish = visibilityScope(t, e, newObserver)
	err := core.HostOf(scope).Contrib().Pgsurgery(node, uint32(freshRef.Address.ObjectID), input)
	if !errors.Is(err, ids.ErrCatalogReplaced) || fresh.Headers != nil || fresh.Generation != generation || original.Headers != headers {
		t.Fatal("old admitted routine affected replacement incarnation", err)
	}
	_ = finish(err)
}

// Err observes actual immutable publication, not poll counts or elapsed time.
// It runs on the operation goroutine; no peer mutates this heap in the unit.
type surgeryPublishedGroupContext struct {
	context.Context
	store    *storage.Relation
	version  ids.VersionID
	cancel   context.CancelFunc
	canceled bool
}

func (c *surgeryPublishedGroupContext) Err() error {
	if !c.canceled && c.store.Headers.Header(c.version).Slot == storage.HeapSlotDead {
		c.canceled = true
		c.cancel()
	}
	return c.Context.Err()
}

func TestPgsurgeryKillCompletedGroupCancellation(t *testing.T) {
	e, ref, _, firstInput := surgeryPublicationFixture(t)
	node := surgeryKillExpression(t, e)
	store := e.Storage.Relations[ref.Address]
	first := store.Versions[0]
	firstBlock, _, err := storage.LogicalTID(first.Version)
	if err != nil {
		t.Fatal(err)
	}
	// Advance the genuine allocator to the next logical block, then let the
	// ordinary INSERT create its real tuple. Reserved IDs create no slots.
	e.Mu.Lock()
	e.Storage.SetFrontierLocked(e.Storage.RowFrontier(), (uint64(firstBlock)+1)*65535)
	e.Mu.Unlock()
	enginetest.Run(t, e, "INSERT INTO surgery_heap VALUES(2)")
	second := store.Versions[1]
	secondBlock, _, err := storage.LogicalTID(second.Version)
	if err != nil || secondBlock == firstBlock {
		t.Fatal("fixture lacks distinct genuine logical groups", err)
	}
	array, _ := e.CatalogRead().LookupType(sqltype.ID(1010))
	secondInput := surgeryKillInput(t, e, second)
	input, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{secondInput.ArrayElement(0), firstInput.ArrayElement(0)})
	if err != nil {
		t.Fatal(err)
	}
	before := *store
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active := &surgeryPublishedGroupContext{Context: ctx, store: store, version: first.Version, cancel: cancel}
	scope.Op.Ctx = active
	err = core.HostOf(scope).Contrib().Pgsurgery(node, uint32(ref.Address.ObjectID), input)
	if !errors.Is(err, context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) || !active.canceled {
		t.Fatal("actual completed-group cancellation was not observed", err)
	}
	published := store.Headers
	if published == before.Headers || store.Generation != before.Generation+1 || published.Header(first.Version).Slot != storage.HeapSlotDead || published.Header(second.Version).Slot != storage.HeapSlotNormal || published.Apply(second).HeapKilled || before.Headers.Apply(first).HeapKilled || !storage.SameVersionRoot(store.Versions, before.Versions) {
		t.Fatal("canceled suffix published, first group vanished or old capture mutated")
	}
	if err := finish(err); !errors.Is(err, context.Canceled) {
		t.Fatal("owner disposal lost cancellation", err)
	}
	if store.Headers != published || !published.Apply(first).HeapKilled || before.Headers.Apply(first).HeapKilled || &published.Apply(first).Values[0] != &first.Values[0] {
		t.Fatal("owner rollback undid first group or changed captured backing")
	}
	visible := storage.VisibleHeapVersions(store, e.Visibility.CommitSeq())
	if len(visible) != 1 || visible[0].Version != second.Version {
		t.Fatal("completed-group cancellation did not preserve the exact suffix tuple", visible)
	}
}
