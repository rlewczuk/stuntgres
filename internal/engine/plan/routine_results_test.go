package plan

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineResultSourceOccurrence(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	definition := &proc.Definition{ReturnsSet: true}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Usage: integer.Usage()}, {Mode: "o", Name: "first", Usage: integer.Usage()}, {Mode: "o", Name: "second", Usage: integer.Usage()}}); err != nil {
		t.Fatal(err)
	}
	shape, err := definition.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	definition.Result = shape.Usage()
	procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, SQL: definition, Result: 2249}
	domain := new(ids.Domain)
	argument := LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	node := SQLRoutineExpr{Info: expr.Info{Usage: shape.Usage(), RoutineReference: procedure}, Procedure: procedure, Ref: domain.Ref(1, procedure.Address()), Args: []expr.Expr{argument}, WrittenCount: 1, InputMap: []RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
	rebuilt, err := node.Rebuild(node.Info, []expr.Expr{argument})
	if err != nil {
		t.Fatal(err)
	}
	if admitted, set := node.SourceAdmission(); !admitted || !set || !node.SameSourceOccurrence(rebuilt) {
		t.Fatal("reconstruction lost genuine record-set occurrence")
	}
	other := node
	other.Ref.Epoch++
	if node.SameSourceOccurrence(other) {
		t.Fatal("different reference became same occurrence")
	}
	other = node
	copy := *definition
	otherProcedure := *procedure
	otherProcedure.SQL = &copy
	other.Procedure = &otherProcedure
	if node.SameSourceOccurrence(other) {
		t.Fatal("different declaration became same occurrence")
	}
	other = node
	other.InputMap = []RoutineInput{{Start: 0, Count: 1, Expanded: true}}
	if node.SameSourceOccurrence(other) {
		t.Fatal("different input assembly became same occurrence")
	}
	copy.ReturnsSet = false
	other = node
	other.Procedure = &otherProcedure
	if admitted, set := other.SourceAdmission(); !admitted || set {
		t.Fatal("scalar result gained set cardinality")
	}
}
