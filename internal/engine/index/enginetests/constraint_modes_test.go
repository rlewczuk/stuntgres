package enginetests

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func TestConstraintModesPrecedence(t *testing.T) {
	var modes index.ConstraintModes
	if !modes.Zero() || !modes.Deferred(1, true, true) || modes.Deferred(1, true, false) || modes.Deferred(1, false, true) {
		t.Fatal("definition/default/zero invariant")
	}
	modes.AllSet, modes.AllDeferred = true, true
	modes.ByConstraint = map[ids.OID]bool{1: false}
	if modes.Deferred(1, true, true) || !modes.Deferred(2, true, false) || modes.Deferred(2, false, false) {
		t.Fatal("named/ALL/eligibility precedence")
	}
	modes.AllDeferred = false
	modes.ByConstraint[1] = true
	if !modes.Deferred(1, true, false) || modes.Deferred(2, true, true) {
		t.Fatal("named override did not supersede ALL")
	}
}

func TestConstraintEventSliceOwnership(t *testing.T) {
	original := []index.ConstraintEvent{{Constraint: 1, Row: 1, Version: 1}}
	// Ensure spare capacity exists: append must not mutate a saved owner's tail.
	backing := make([]index.ConstraintEvent, 2, 4)
	copy(backing, original)
	backing[1] = index.ConstraintEvent{Constraint: 99}
	modes := index.ConstraintModes{Events: backing[:1]}
	scope := index.Scope{ConstraintModes: modes}
	payload := &index.ForeignKeyEvent{Next: storage.RowVersion{Row: 2, Version: 2}}
	scope.AppendConstraintEvent(index.ConstraintEvent{Constraint: 2, Row: 2, Version: 2, ForeignKey: payload})
	if backing[1].Constraint != 99 || len(modes.Events) != 1 || len(scope.ConstraintModes.Events) != 2 {
		t.Fatal("command suffix mutated captured owner backing")
	}
	if scope.ConstraintModes.Events[1].ForeignKey != payload {
		t.Fatal("immutable concrete FK payload was copied/reconstructed")
	}
	mark := index.ExclusionMarkSlotOf(scope.ConstraintModes, nil)
	got := index.ExclusionMarkOrZero(mark)
	if !reflect.DeepEqual(got.Modes, scope.ConstraintModes) || got.Modes.Events[1].ForeignKey != payload {
		t.Fatal("savepoint lost original payload ownership")
	}
}

func TestConstraintEventConsumedMarkOwnership(t *testing.T) {
	payload := &index.UserTriggerEvent{Targets: []int32{2}}
	original := []index.ConstraintEvent{{Constraint: 1}, {Constraint: 2, UserTrigger: payload}, {Constraint: 3}}
	scope := index.Scope{ConstraintModes: index.ConstraintModes{Events: original}}
	mark := index.ExclusionMarkSlotOf(scope.ConstraintModes, nil)
	event := scope.ClaimConstraintEvent(1)
	if event.UserTrigger != payload || len(scope.ConstraintModes.Events) != 2 || original[1].UserTrigger != payload || original[2].Constraint != 3 {
		t.Fatal("claim mutated a retained savepoint's membership or payload")
	}
	scope.AppendConstraintEvent(index.ConstraintEvent{Constraint: 4})
	scope.ConstraintModes = index.ExclusionMarkOrZero(mark).Modes
	// A restored vector may be marked owned by a previous suffix. Claims
	// must remain safe even then, including prefix/suffix slice-only removal.
	scope.ClaimConstraintEvent(0)
	scope.ClaimConstraintEvent(1)
	scope.AppendConstraintEvent(index.ConstraintEvent{Constraint: 5})
	if len(scope.ConstraintModes.Events) != 2 || scope.ConstraintModes.Events[0].UserTrigger != payload || len(original) != 3 || original[0].Constraint != 1 || original[2].Constraint != 3 {
		t.Fatal("rollback/claim/reappend overwrote original event backing")
	}
}
