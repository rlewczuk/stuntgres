package tablecmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
)

// Captures mutate the statement's input in place; Input must read it live.
func TestAlterHostInputReflectsCheckSnapshotCapture(t *testing.T) {
	e := engine.New()
	op, _ := conversionUnitAuth(t, e)
	scope := &core.StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	var h table.AlterHost = tablecmds.AlterHost{TableHost: tablecmds.TableHost{core.DDLHost{E: e, Op: op}}, Scope: scope}
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
