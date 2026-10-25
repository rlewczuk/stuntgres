package analyze

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQuerySetScalarExpansionOwnership(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
			break
		}
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
		{Attribute: 1, Name: "a", Usage: text.Usage()},
		{Attribute: 2, Name: "b", Usage: text.Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := expr.Info{Usage: text.Usage(), Pos: parser.Position{Character: 8}}
	base := plan.ColumnExpr{Info: expr.Info{Usage: shape.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	fields := []plan.FieldExpr{
		{Info: info, Base: base, Attribute: 1, Index: 0},
		{Info: info, Base: base, Attribute: 2, Index: 1},
	}
	for _, upper := range []bool{false, true} {
		node := &plan.SetProjectNode{ScalarUpper: upper}
		for i, field := range fields {
			reserved := plan.ColumnExpr{Info: info, Scope: 1, Frame: 1, Slot: i + 1}
			node.ScalarBindings = append(node.ScalarBindings, plan.SetSlot{Original: field, Column: reserved, Scheduled: !upper})
		}
		for i, field := range fields {
			got, changed := QueryScalarOwner(node, field)
			if !changed {
				t.Fatal("expanded field lost scalar owner", i, upper)
			}
			if upper {
				if owned, ok := got.(plan.FieldExpr); !ok || owned.Attribute != field.Attribute {
					t.Fatal("expanded field borrowed sibling expression", i, got)
				}
			} else if owned, ok := got.(plan.ColumnExpr); !ok || owned.Slot != i+1 {
				t.Fatal("expanded field borrowed sibling reservation", i, got)
			}
		}
	}
}
