package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPartitionRoutingPublication(t *testing.T) {
	t.Run("captured_deletion_authority", partitionRoutingPublicationCapturedDeletionAuthority)
	t.Run("private_movement_copy", partitionRoutingPublicationPrivateMovementCopy)
	for _, phase := range []string{"same_leaf", "move", "cancel_after_delete", "finish_veto", "latest_captured_delete", "stale_destination_binding"} {
		t.Run(phase, func(t *testing.T) {
			e, root, _, leaf, destination := partitionRoutingUnitEngine(t)
			scope, b, source := partitionRoutingUnitScope(t, e, root, false)
			old := e.Storage.Relations[leaf.Address()].Versions[0]
			retained := source.View.Retain()
			if retained == nil {
				t.Fatal("missing original snapshot")
			}
			defer retained.Release()
			partitionRoutingUnitAssignment(t, &b, 2)
			if phase == "same_leaf" {
				partitionRoutingUnitAssignment(t, &b, 1)
			}
			if phase == "latest_captured_delete" {
				// The frozen candidate keeps version21, while the canonical Delete
				// must end the actual latest same-RowID predecessor under its lock.
				latest := old
				latest.Version = e.Storage.AllocateVersionLocked()
				latest.Born = 2
				e.Storage.Relations[leaf.Address()].Versions = []storage.RowVersion{latest}
				e.Visibility.SetCommitSeqLocked(2)
				old = latest
			}
			underlying, cancel := context.WithCancel(scope.Op.Context())
			defer cancel()
			checkpoint := &rangePartitionPhaseContext{Context: underlying, target: "dml.ApplyValidated", effect: func() {
				deletion := scope.Stage.Deletes[leaf.Address()]
				if deletion == nil || !deletion.Rows[old.Row].Moved {
					t.Error("destination insertion preceded canonical source deletion")
				}
				if phase == "stale_destination_binding" {
					e.Mu.Lock()
					e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
					e.Mu.Unlock()
				} else {
					cancel()
				}
			}}
			if phase == "cancel_after_delete" || phase == "stale_destination_binding" {
				scope.Op.Ctx = checkpoint
			}
			out, err := dml.ExecuteUpdate(scope.WriteHost(), dml.UpdateInput{Context: scope.Op.Context(), Binding: b, Source: source, Schema: "public"})
			if phase == "cancel_after_delete" {
				if !checkpoint.entered.Load() || !errors.Is(err, context.Canceled) || out.Count != 0 {
					t.Fatal("cancellation missed actual post-delete checkpoint", out, err)
				}
			} else {
				if err != nil || out.Count != 1 {
					t.Fatal(out, err)
				}
				if phase == "stale_destination_binding" && (!checkpoint.entered.Load() || scope.UniqueBinding.Catalog != e.Catalog.State) {
					t.Fatal("stale destination binding was not revalidated")
				}
				if phase == "same_leaf" {
					if next := scope.Stage.Rows[leaf.Address()][0]; next.Row != old.Row || next.Version == old.Version {
						t.Fatal("same-leaf UPDATE changed physical identity", next)
					}
					if scope.Stage.Deletes[leaf.Address()] != nil {
						t.Fatal("same-leaf UPDATE staged movement")
					}
				} else {
					deletion := scope.Stage.Deletes[leaf.Address()].Rows[old.Row]
					next := scope.Stage.Rows[destination.Address()][0]
					if !deletion.Moved || deletion.Version != old.Version || next.Row == old.Row || next.Version == old.Version || next.Base != 0 {
						t.Fatal("invalid movement lineage", deletion, next)
					}
				}
			}
			veto := errors.New("publication veto")
			if phase == "finish_veto" {
				err = veto
			}
			finishErr := scope.Finish(err)
			if !errors.Is(finishErr, err) {
				t.Fatal("Finish changed failure", finishErr, err)
			}
			if err != nil {
				if len(e.Storage.Relations[destination.Address()].Versions) != 0 || e.Storage.Relations[leaf.Address()].Ends != nil {
					t.Fatal("failed stage published one side")
				}
			} else if phase != "same_leaf" {
				store := e.Storage.Relations[leaf.Address()]
				if end := store.Ends.Rows[old.Row]; !end.Moved || end.Version != old.Version || end.End == 0 {
					t.Fatal("committed movement provenance lost", end)
				}
			}
			// The original retained physical prefix still has its original row,
			// independent of the current committed deletion root.
			prior, err := exec.CaptureSource(context.Background(), retained, b.Ref.Domain().Ref(b.Ref.Epoch, leaf.Address()))
			if err != nil {
				t.Fatal(err)
			}
			if prior.Count() != 1 || prior.VersionAt(prior.RawIndex(0)).Version != 21 {
				t.Fatal("retained source changed")
			}
			prior.Close()
			lockEmpty(t, e)
		})
	}
}

func partitionRoutingPublicationCapturedDeletionAuthority(t *testing.T) {
	e, root, _, leaf, _ := partitionRoutingUnitEngine(t)
	scope, b, source := partitionRoutingUnitScope(t, e, root, false)
	physical := dml.MutationPhysicalRow(source, 0, b)
	captured := physical.Deletion()
	if err := scope.WriteHost().AcquireUpdateRow(physical.Leaf.Ref, captured.Row, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	store := e.Storage.Relations[leaf.Address()]
	latest := store.Versions[0]
	latest.Version = e.Storage.AllocateVersionLocked()
	store.Versions = []storage.RowVersion{latest}
	bad := captured
	bad.Version = latest.Version
	bad.Moved = true
	if deleted, err := scope.Writes().Delete(bad); err == nil || deleted {
		t.Fatal("latest identity bypassed captured candidate gate")
	}
	captured.Moved = true
	if deleted, err := scope.Writes().Delete(captured); err != nil || !deleted {
		t.Fatal("captured predecessor was not refreshed canonically", deleted, err)
	}
	if staged := scope.Stage.Deletes[leaf.Address()].Rows[captured.Row]; staged.Version != latest.Version || !staged.Moved {
		t.Fatal("latest movement end not staged", staged)
	}
	if err := scope.Finish(errors.New("discard authority probe")); err == nil {
		t.Fatal("missing discard error")
	}
	lockEmpty(t, e)
}

func partitionRoutingPublicationPrivateMovementCopy(t *testing.T) {
	// The immutable private overlay copied into command/savepoint roots retains
	// movement provenance as a value, without a separate global move registry.
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 26000}
	before := &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{7: {Row: 7, Version: 9, Moved: true}}}
	mark := map[ids.ObjectAddress]*visibility.PrivateDeletes{address: before}
	staged := map[ids.ObjectAddress]*visibility.PrivateDeletes{address: {Rows: map[ids.RowID]visibility.DeleteCandidate{8: {Row: 8, Version: 10}}}}
	merged := visibility.MergeDeletions(mark, staged)
	if !merged[address].Rows[7].Moved || merged[address].Rows[8].Moved || len(merged[address].Rows) != 2 {
		t.Fatal("private publication lost movement provenance", merged[address])
	}
	if len(mark[address].Rows) != 1 || !mark[address].Rows[7].Moved || mark[address] != before {
		t.Fatal("merge mutated the savepoint's immutable deletion root")
	}
}
