package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// Exercise actual view-to-heap selection, including the fast unfiltered prefix.
func snapshotUnitRows(t *testing.T, scope *StatementScope, ref ids.ObjectRef, want []ids.RowID) {
	t.Helper()
	captured, err := scope.AcquireDataView(ref)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Frontier() != ScopeSnapshotFrontier(scope) {
		t.Fatal("physical source advanced the active frontier")
	}
	source, err := exec.CaptureSource(scope.Op.Context(), captured, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var rows []ids.RowID
	for i := range source.Count() {
		rows = append(rows, source.VersionAt(source.RawIndex(i)).Row)
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatal("tuple selection", rows, "want", want)
	}
}

func TestSnapshotAuthoritativeCapture(t *testing.T) {
	for _, singleton := range []bool{false, true} {
		t.Run(map[bool]string{false: "physical", true: "singleton"}[singleton], func(t *testing.T) {
			e := New()
			stage, relation := queryCreateStage(t, e, "capture_rows", 1)
			queryCreatePublish(t, e, stage)
			_, peer := blockingUnitOwner(t, e)
			_, bumper := blockingUnitOwner(t, e)
			pgLocksUnitAssign(t, e, peer)
			pgLocksUnitAssign(t, e, bumper)
			e.Mu.Lock()
			e.Visibility.SetCommitSeqLocked(10)
			e.Visibility.EndTransactionIdentityLocked(bumper.Ref(), true)
			store := e.Storage.Relations[relation.Address()]
			store.Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 10}}
			e.Mu.Unlock()
			observer, _ := blockingUnitOwner(t, e)
			scope, _, _ := concurrentUnitSnapshotScope(t, e, observer)
			if err := ScopeSnapshotExecutionAdmission(scope, &parser.Select{}); err != nil {
				t.Fatal(err)
			}
			if ScopeCurrentSnapshot(scope) != nil {
				t.Fatal("RC dispatch selected a pre-admission snapshot")
			}
			e.Mu.Lock()
			store.Versions = append(store.Versions, storage.RowVersion{Row: 2, Version: 2, Born: 11})
			e.Visibility.AdvanceCommitSeqLocked()
			e.Visibility.EndTransactionIdentityLocked(peer.Ref(), true)
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), relation.Address())
			var leaves []exec.Leaf
			if !singleton {
				leaves = []exec.Leaf{{Ref: ref, Relation: relation, Storage: store}}
			}
			// The admitted source tree, including a no-FROM singleton, chooses
			// the same pair before constructing any physical source.
			source, err := e.CaptureQuerySourcesLocked(scope, leaves, singleton)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if ScopeCurrentSnapshot(scope) == nil || !ScopeCurrentSnapshot(scope).Visible(uint64(peer.AssignedFullXID())) || ScopeSnapshotFrontier(scope) != 11 {
				t.Fatal("source admission did not select one current pair")
			}
			if !singleton {
				if err := exec.SelectQuerySources(scope.Op.Context(), source); err != nil {
					t.Fatal(err)
				}
				if source.View.Frontier() != 11 || source.Count() != 2 || source.VersionAt(source.RawIndex(1)).Row != 2 {
					t.Fatal("source frontier disagrees with public completion")
				}
			}
		})
	}
}

func TestSnapshotRefreshCapture(t *testing.T) {
	e := New()
	target := refreshUnitRelation(t, e)
	stage, relation := queryCreateStage(t, e, "utility_capture_rows", 1)
	queryCreatePublish(t, e, stage)
	holder, holderRef := blockingUnitOwner(t, e)
	_, bumper := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, holderRef)
	pgLocksUnitAssign(t, e, bumper)
	targetRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), target.Address())
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: targetRef}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	e.Visibility.SetCommitSeqLocked(10)
	e.Visibility.EndTransactionIdentityLocked(bumper.Ref(), true)
	store := e.Storage.Relations[relation.Address()]
	store.Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 10}, {Row: 2, Version: 2, Born: 10}}
	e.Mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := refreshUnitScope(t, e, ctx)
	stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: target.Name()}}}}
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		e.EndLockOwner(holder.Transactions.Ticket)
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
	}()
	go func() { done <- ScopeAcquireMaterializedRefresh(scope, stmt) }()
	lockPending(t, e, 1)
	// Publication while the real utility target lock is pending must not move
	// either half of its active snapshot. Roots are intentionally obtained later.
	e.Mu.Lock()
	store.Versions = append(store.Versions, storage.RowVersion{Row: 3, Version: 3, Born: 11})
	store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 11}}}
	e.Visibility.AdvanceCommitSeqLocked()
	e.Visibility.EndTransactionIdentityLocked(holderRef.Ref(), true)
	e.Mu.Unlock()
	e.EndLockOwner(holder.Transactions.Ticket)
	lockAwait(t, done, nil)
	joined = true
	id := uint64(holderRef.AssignedFullXID())
	if ScopeCurrentSnapshot(scope) == nil || ScopeCurrentSnapshot(scope).Visible(id) || ScopeSnapshotFrontier(scope) != 10 {
		t.Fatal("utility pair refreshed across target wait")
	}
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), relation.Address())
	snapshotUnitRows(t, scope, ref, []ids.RowID{1, 2})
	data := scope.View.Data()
	if len(data.Committed[ref.Address]) != 3 || !data.NeedsBirthFilterAt(ref.Address) || data.Ends[ref.Address] != store.Ends {
		t.Fatal("late physical capture did not retain and filter real birth/end roots")
	}
	observer, _ := blockingUnitOwner(t, e)
	next, _, _ := concurrentUnitSnapshotScope(t, e, observer)
	snapshotUnitRows(t, next, ref, []ids.RowID{1, 3})
	if ScopeSnapshotFrontier(next) != 11 || !ScopeCurrentSnapshot(next).Visible(id) || next.View.Data().NeedsBirthFilterAt(ref.Address) {
		t.Fatal("fresh RC capture did not use the completed publication")
	}
}

func TestSnapshotPartitionUtilityCapture(t *testing.T) {
	for _, isolation := range []session.Isolation{session.RepeatableRead, session.Serializable} {
		t.Run(isolationName(isolation), func(t *testing.T) {
			e := New()
			parent := partitionAttachUnitRoot(t, e, "capture_parent", catalogUnitOID(t, e))
			partitionAttachUnitInstall(t, e, parent)
			stage, relation := queryCreateStage(t, e, "partition_capture_rows", 1)
			queryCreatePublish(t, e, stage)
			_, peer := blockingUnitOwner(t, e)
			_, bumper := blockingUnitOwner(t, e)
			pgLocksUnitAssign(t, e, peer)
			pgLocksUnitAssign(t, e, bumper)
			e.Mu.Lock()
			e.Visibility.SetCommitSeqLocked(10)
			e.Visibility.EndTransactionIdentityLocked(bumper.Ref(), true)
			store := e.Storage.Relations[relation.Address()]
			store.Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 10}}
			e.Mu.Unlock()
			observer, _ := blockingUnitOwner(t, e)
			observer.Transactions.Isolation = isolation
			scope, _, _ := concurrentUnitSnapshotScope(t, e, observer)
			stmt := &parser.CreateTable{
				Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "capture_child"}}},
				PartitionOf: &parser.PartitionParent{
					Name:  parser.ObjectName{Parts: []parser.Identifier{{Text: parent.Name()}}},
					Bound: parser.PartitionBound{Default: true},
				},
			}
			if err := scope.AcquireRelation(stmt, true); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			store.Versions = append(store.Versions, storage.RowVersion{Row: 2, Version: 2, Born: 11})
			e.Visibility.AdvanceCommitSeqLocked()
			e.Visibility.EndTransactionIdentityLocked(peer.Ref(), true)
			e.Mu.Unlock()
			if ScopeCurrentSnapshot(scope) == nil || ScopeCurrentSnapshot(scope) != scope.Owner.CurrentSnapshot || ScopeCurrentSnapshot(scope).Visible(uint64(peer.AssignedFullXID())) || ScopeSnapshotFrontier(scope) != 10 {
				t.Fatal("partition utility did not retain its first stable pair")
			}
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), relation.Address())
			snapshotUnitRows(t, scope, ref, []ids.RowID{1})
		})
	}
}
