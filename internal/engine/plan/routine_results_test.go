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
	node.Signature = proc.InvocationSignature{Inputs: definition.Inputs, Result: definition.Result}
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

func TestSQLRoutineStoredResultSpecialization(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	definition := &proc.Definition{ReturnsSet: true}
	if err := definition.SetFormals([]proc.Formal{
		{Mode: "i", Name: "x", Usage: types[2283].Usage()},
		{Mode: "o", Name: "first", Usage: types[2283].Usage()},
		{Mode: "o", Name: "label", Usage: types[sqltype.Text].Usage()},
	}); err != nil {
		t.Fatal(err)
	}
	shape, err := definition.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	definition.Result = shape.Usage()
	result, err := definition.ResolveResult(func(usage sqltype.Usage) sqltype.Usage {
		if usage.Type == types[2283] {
			return types[sqltype.Int4].Usage()
		}
		return usage
	})
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, SQL: definition, Result: 2249}
	domain := new(ids.Domain)
	argument := LiteralExpr{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, Value: sqltype.Null(types[sqltype.Int4])}
	call := SQLRoutineExpr{
		Info: expr.Info{Usage: result, RoutineReference: procedure},
		Procedure: procedure, Ref: domain.Ref(1, procedure.Address()),
		Args: []expr.Expr{argument}, WrittenCount: 1,
		InputMap: []RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0},
		Signature: proc.InvocationSignature{Inputs: []sqltype.Usage{argument.Usage}, Result: result},
	}
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("accepted concrete result rejected", err)
	}
	for _, index := range []int{0, 1} {
		members := []sqltype.CompositeMember{result.Type.CompositeMember(0), result.Type.CompositeMember(1)}
		foreign := *members[index].Usage.Type
		members[index].Usage.Type = &foreign
		forged, err := sqltype.RecordShape(members)
		if err != nil {
			t.Fatal(err)
		}
		invalid := call
		invalid.Usage, invalid.Signature.Result = forged.Usage(), forged.Usage()
		if err := invalid.ValidateStoredProvider(); err == nil {
			t.Fatal("foreign same-OID output generation admitted", index)
		}
	}
	private, err := definition.Specialize(call.Signature)
	if err != nil {
		t.Fatal(err)
	}
	fixed := *procedure
	fixed.SQL = private
	call.Procedure, call.Info.RoutineReference = &fixed, &fixed
	foreignShape := *result.Type
	call.Usage, call.Signature.Result = foreignShape.Usage(), foreignShape.Usage()
	if err := call.ValidateStoredProvider(); err == nil {
		t.Fatal("fixed output occurrence lost declaration identity")
	}
	if definition.Result.Type != shape || definition.Formals[1].Usage != types[2283].Usage() {
		t.Fatal("stored validation mutated original declaration")
	}
	generic := &proc.Definition{Result: types[2249].Usage()}
	genericProcedure := *procedure
	genericProcedure.SQL = generic
	for _, usage := range []sqltype.Usage{sqltype.DeferredRecord().Usage(), result} {
		recordCall := SQLRoutineExpr{
			Info: expr.Info{Usage: usage, RoutineReference: &genericProcedure},
			Procedure: &genericProcedure, Ref: call.Ref,
			Signature: proc.InvocationSignature{Result: usage},
		}
		if err := recordCall.ValidateStoredProvider(); err != nil {
			t.Fatal("unshaped RECORD completion lost declaration provenance", err)
		}
	}
}
