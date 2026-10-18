package engine

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionAttachUnitBegin(t *testing.T, e *Engine, session *Session, isolation Isolation) ids.TxRef {
	t.Helper()
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ref, err := e.Begin(op, TxOptions{Isolation: isolation})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if session.TransactionState() != TxIdle {
			op, err := session.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer op.Done()
			if _, err := e.Rollback(op, ref); err != nil {
				t.Error(err)
			}
		}
	})
	return ref
}

func partitionAttachUnitWrite(t *testing.T, e *Engine, session *Session, ref ids.ObjectRef, values ...int64) {
	t.Helper()
	scope, op := partitionAttachUnitScope(t, e, session)
	rows := make([][]sqltype.Value, len(values))
	d := e.catalog.state.MustRelation(ref.Address)
	for i, n := range values {
		value, err := sqltype.Integer(d.columns[0].Usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{value}
	}
	if err := scope.Writes().Apply(ref, rows); err != nil {
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
}

func partitionAttachUnitValues(t *testing.T, view *VisibilityView, ref ids.ObjectRef) []int64 {
	t.Helper()
	source, err := captureQuerySource(context.Background(), view, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	var values []int64
	for i := range source.count() {
		value, err := source.row(i)[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}

func TestPartitionAttachTransactionOverlay(t *testing.T) {
	t.Run("latest_validation_preserves_rr_and_owner_effects", func(t *testing.T) {
		e := New()
		child := inheritanceUnitRelation(e, "attach_rr_child", 46200, "k")
		child.relationKind = tableRelation
		partitionAttachUnitInstall(t, e, child)
		ref := e.catalog.domain.Ref(e.catalog.state.epoch, child.address)
		writer, owner := sessionStart(t, e, nil), sessionStart(t, e, nil)
		partitionAttachUnitWrite(t, e, writer, ref, 1)
		partitionAttachUnitBegin(t, e, owner, RepeatableRead)
		first, firstOp := partitionAttachUnitScope(t, e, owner)
		ordinary, err := first.AcquireDataView(ref)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(partitionAttachUnitValues(t, ordinary, ref), []int64{1}) {
			t.Fatal("initial snapshot")
		}
		if err := first.Finish(nil); err != nil {
			t.Fatal(err)
		}
		firstOp.Done()
		frontier, roots := owner.transactions.frontier, maps.Clone(owner.transactions.roots)
		partitionAttachUnitWrite(t, e, writer, ref, 3)
		partitionAttachUnitWrite(t, e, owner, ref, 2)
		deleting, deleteOp := partitionAttachUnitScope(t, e, owner)
		deleteSyntax := &parser.Delete{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: child.name}}}}
		if err := deleting.acquireRelation(deleteSyntax, true); err != nil {
			t.Fatal(err)
		}
		if _, err := deleting.AcquireDataView(ref); err != nil {
			t.Fatal(err)
		}
		store := e.storage[ref.Address]
		row := store.versions[0]
		candidate := deleteCandidate{ref: ref, storage: store, row: row.row, version: row.version, ordinal: 0}
		if err := deleting.acquireRow(candidate); err != nil {
			t.Fatal(err)
		}
		deleted, err := deleting.Writes().Delete(candidate)
		if err != nil || !deleted {
			t.Fatal(deleted, err)
		}
		if err := deleting.Finish(nil); err != nil {
			t.Fatal(err)
		}
		deleteOp.Done()
		validation, validationOp := partitionAttachUnitScope(t, e, owner)
		ordinary, err = validation.AcquireDataView(ref)
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		latest, err := validation.acquirePartitionValidationViewsLocked(e, []ids.ObjectRef{ref})
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if ordinary == latest || latest.Tx() != ordinary.Tx() || latest.Command() != ordinary.Command() || latest.Frontier() <= ordinary.Frontier() {
			t.Fatal("validation did not retain same owner with a separate latest frontier")
		}
		if got := partitionAttachUnitValues(t, ordinary, ref); !reflect.DeepEqual(got, []int64{2}) {
			t.Fatal("ordinary RR changed", got)
		}
		if got := partitionAttachUnitValues(t, latest, ref); !reflect.DeepEqual(got, []int64{3, 2}) {
			t.Fatal("latest view lost peer row/owner write/delete", got)
		}
		if owner.transactions.frontier != frontier || !reflect.DeepEqual(owner.transactions.roots, roots) {
			t.Fatal("validation advanced ordinary snapshot roots")
		}
		retained := latest.Retain()
		defer retained.Release()
		if err := validation.Finish(nil); err != nil {
			t.Fatal(err)
		}
		validationOp.Done()
		if latest.data.Load() != nil || ordinary.data.Load() != nil || !reflect.DeepEqual(partitionAttachUnitValues(t, retained, ref), []int64{3, 2}) {
			t.Fatal("Finish did not release only its own view handles")
		}
	})
	for _, end := range []string{"rollback", "savepoint", "commit"} {
		t.Run(end, func(t *testing.T) {
			e := New()
			parent := partitionAttachUnitRoot(t, e, "attach_tx_parent", 46210)
			child := inheritanceUnitRelation(e, "attach_tx_child", 46211, "k")
			child.relationKind = tableRelation
			partitionAttachUnitInstall(t, e, parent, child)
			parent = e.catalog.state.MustRelation(parent.address)
			child = e.catalog.state.MustRelation(child.address)
			owner := sessionStart(t, e, nil)
			tx := partitionAttachUnitBegin(t, e, owner, ReadCommitted)
			parentRef := e.catalog.domain.Ref(e.catalog.state.epoch, parent.address)
			childRef := parentRef
			childRef.Address = child.address
			before, beforeOp := partitionAttachUnitScope(t, e, owner)
			partitionAttachUnitGrant(t, before, parentRef, accessShare)
			if err := before.Finish(nil); err != nil {
				t.Fatal(err)
			}
			beforeOp.Done()
			control := &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: parser.Identifier{Text: "before_attach"}}
			mark, markOp := partitionAttachUnitScope(t, e, owner)
			if _, err := mark.executeSavepoint(control, 0); err != nil {
				t.Fatal(err)
			}
			if err := mark.Finish(nil); err != nil {
				t.Fatal(err)
			}
			markOp.Done()
			attach, attachOp := partitionAttachUnitScope(t, e, owner)
			partitionAttachUnitGrant(t, attach, parentRef, shareUpdateExclusive)
			partitionAttachUnitGrant(t, attach, childRef, accessExclusive)
			stage := partitionAttachUnitStage(t, e, attach.ResolutionCatalog(), parent, child)
			changes := cloneCatalogChanges(attach.owner.catalog)
			if err := e.stageCatalogOverlay(attach, stage, changes); err != nil {
				t.Fatal(err)
			}
			if err := attach.Finish(nil); err != nil {
				t.Fatal(err)
			}
			attachOp.Done()
			private := e.privateCatalogLocked(owner.transactions.catalog)
			if private.MustRelation(child.address).membership == nil || e.catalog.state.Get(child.address) != child || len(parent.partition.children) != 0 {
				t.Fatal("overlay was not private and reciprocal")
			}
			if end == "savepoint" {
				rollback, rollbackOp := partitionAttachUnitScope(t, e, owner)
				control.Kind = parser.RollbackSavepoint
				if _, err := rollback.executeSavepoint(control, 0); err != nil {
					t.Fatal(err)
				}
				if err := rollback.Finish(nil); err != nil {
					t.Fatal(err)
				}
				rollbackOp.Done()
				if e.privateCatalogLocked(owner.transactions.catalog).Get(child.address) != child {
					t.Fatal("savepoint retained attachment overlay")
				}
				lockState := e.locks.Inspect()
				parentMode := lockState.Resources[lockResource{ObjectRef: parentRef}].Holders[owner.transactions.ticket].Mode
				childEntry := lockState.Resources[lockResource{ObjectRef: childRef}]
				childHeld := childEntry != nil && childEntry.Holders[owner.transactions.ticket].Mode != 0
				if parentMode != accessShare || childHeld {
					t.Fatal("savepoint did not restore earlier lock contribution", parentMode, childHeld)
				}
			}
			op, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if end == "commit" {
				_, err = e.Commit(op, tx)
			} else {
				_, err = e.Rollback(op, tx)
			}
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			attached := e.catalog.state.MustRelation(child.address).membership != nil
			if attached != (end == "commit") {
				t.Fatal("transaction end published wrong topology", attached)
			}
			if owner.transactions.catalog != nil {
				t.Fatal("transaction end leaked private overlay")
			}
			lockEmpty(t, e)
		})
	}
}
