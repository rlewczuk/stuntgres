package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPrivateDeletionProjectionCollisions(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 42}
	original, replacement := &storage.Relation{}, &storage.Relation{}
	root := &visibility.PrivateDeletes{}
	first := visibility.DeleteCandidate{Row: 1, Version: 11, Base: 1, Storage: original, RepairStamp: 3, BaseRepairStamp: 2, EndFullXID: 99}
	second := visibility.DeleteCandidate{Row: 1, Version: 12, Base: 2, Storage: original, RepairStamp: 5, BaseRepairStamp: 4, EndFullXID: 100, Moved: true}
	root.Set(first)
	root.Set(second)
	owner := &session.TransactionOwner{Deletes: map[ids.ObjectAddress]*visibility.PrivateDeletes{address: root}}
	mark := owner.CaptureSavepoint(session.SavepointMark{Name: "physical_siblings"})
	projected := session.ProjectDeletions(root, replacement)
	if projected == root || projected.Len() != 2 || mark.Deletes[address] != root {
		t.Fatal("projection lost immutable owner/savepoint roots")
	}
	for _, expected := range []visibility.DeleteCandidate{first, second} {
		actual, ok := projected.Lookup(expected.Row, expected.Version)
		if !ok || actual.Storage != replacement || actual.Version != expected.Version || actual.Base != expected.Base || actual.RepairStamp != expected.RepairStamp || actual.BaseRepairStamp != expected.BaseRepairStamp || actual.EndFullXID != expected.EndFullXID || actual.Moved != expected.Moved {
			t.Fatal("projection omitted/rebound exact sibling or admission facts", actual, expected)
		}
		retained, ok := mark.Deletes[address].Lookup(expected.Row, expected.Version)
		if !ok || retained != expected {
			t.Fatal("projection mutated savepoint-owned candidate", retained, expected)
		}
	}
	clone := projected.Clone()
	first.Storage, first.EndFullXID = replacement, 101
	clone.Set(first)
	clone.Delete(second.Row, second.Version)
	if projected.Len() != 2 || root.Len() != 2 || mark.Deletes[address].Len() != 2 {
		t.Fatal("postprojection mutation reached old owner/backing")
	}
	if projectedSecond, ok := projected.Lookup(second.Row, second.Version); !ok || !projectedSecond.Moved || projectedSecond.EndFullXID != 100 {
		t.Fatal("cloned collision deletion altered earlier projection")
	}
}
