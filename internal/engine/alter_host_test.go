package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
)

// Captures mutate the statement's input in place; Input must read it live.
func TestAlterHostInputReflectsCheckSnapshotCapture(t *testing.T) {
	e := New()
	op, _ := conversionUnitAuth(t, e)
	scope := &StatementScope{op: op, owner: op.Session.Transactions}
	e.mu.Lock()
	defer e.mu.Unlock()
	var h table.AlterHost = alterHost{ddlHost: ddlHost{e: e, op: op}, scope: scope}
	if h.Input().Regcollation != nil {
		t.Fatal("a fresh scope already resolves regcollation input")
	}
	if err := h.CaptureCheckSnapshots(&catalog.Relation{}, false); err != nil {
		t.Fatal(err)
	}
	if h.Input().Regcollation != nil {
		t.Fatal("a relation with no snapshot facts captured a comment snapshot")
	}
	relation := &catalog.Relation{Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{Name: "c", CommentSnapshot: true}}}}
	if err := h.CaptureCheckSnapshots(relation, false); err != nil {
		t.Fatal(err)
	}
	if h.Input().Regcollation == nil {
		t.Fatal("Input does not reflect the CHECK comment snapshot capture")
	}
}
