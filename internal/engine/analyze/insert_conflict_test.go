package analyze

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestInsertConflictCloneOwnsTheClauseHeader(t *testing.T) {
	if (*InsertConflict)(nil).Clone() != nil {
		t.Fatal("Clone of a nil clause must stay nil")
	}
	update := &UpdateBinding{}
	original := &InsertConflict{
		Keys:       []ConflictKey{{Attribute: 2}},
		Constraint: ids.ObjectAddress{ClassID: 2606, ObjectID: 7},
		Reads:      []int{1},
		Targets:    []int{0, 1},
		Update:     update,
		Width:      2,
	}
	clone := original.Clone()
	if clone == original {
		t.Fatal("Clone returned the same clause")
	}
	if clone.Update != update || !slices.Equal(clone.Targets, original.Targets) || clone.Width != 2 || clone.Constraint != original.Constraint || clone.Keys[0].Attribute != 2 {
		t.Fatalf("Clone lost analyzed fields: %+v", clone)
	}
	clone.Update, clone.Width = nil, 5
	if original.Update != update || original.Width != 2 {
		t.Fatal("changing the clone's header changed the original")
	}
}
