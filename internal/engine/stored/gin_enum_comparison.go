package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ginEnumOperands(view catalog.Read, left, right *sqltype.Descriptor) bool {
	if left == nil || left != right || !left.BaseType().IsEnum() {
		return false
	}
	current, ok := view.LookupType(left.ID())
	return ok && current == left
}
func currentGinEnumCompare(n plan.GinEnumCompareExpr, view catalog.Read) (*catalog.Procedure, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if !ginEnumOperands(view, n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type) {
		return nil, fmt.Errorf("stuntgres: foreign stored enum comparator operands")
	}
	result, ok := view.LookupType(sqltype.Int4)
	if !ok || n.ResultUsage().Type != result {
		return nil, fmt.Errorf("stuntgres: foreign stored enum comparator result")
	}
	original, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	return ginEnumProcedure(view, n.Ref, original)
}
func ginEnumProcedure(view catalog.Read, ref ids.ObjectRef, original *catalog.Procedure) (*catalog.Procedure, error) {
	current, symbol, err := currentInstalledProcedure(view, ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockBtreeGin || symbol != "gin_enum_cmp" || current.Signature != "3500,3500" || current.Result != 23 {
		return nil, fmt.Errorf("stuntgres: invalid stored enum comparator declaration")
	}
	return current, nil
}
func remapGinEnumCompare(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, n plan.GinEnumCompareExpr, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	source := n
	source.Ref.Address = captured.Address()
	if err := source.ValidateStoredProvider(); err != nil {
		return err
	}
	owner := view.Owner()
	if owner == nil || n.Ref.Domain() != owner.Domain || n.Ref.Epoch != view.Epoch() || n.Ref.Address.ClassID != ids.ProcedureClassID || n.Ref.Address.SubID != 0 || original == nil || captured.Revision() != original.Revision() || captured.Implementation != extension.StockBtreeGin {
		return fmt.Errorf("stuntgres: invalid mapped enum comparator capture")
	}
	left, right := typ(n.Args[0].ResultUsage().Type), typ(n.Args[1].ResultUsage().Type)
	if !ginEnumOperands(view, left, right) {
		return fmt.Errorf("stuntgres: foreign mapped enum comparator operands")
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, n.Ref.Address)
	declaration, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(declaration, original) {
		return fmt.Errorf("stuntgres: missing remapped enum comparator declaration")
	}
	_, err := ginEnumProcedure(view, n.Ref, original)
	return err
}
func ginEnumCompareRendering(n plan.GinEnumCompareExpr, name string) (expr.Rendering, error) {
	spec, err := n.Render()
	if err != nil {
		return expr.Rendering{}, err
	}
	if spec.Name != name {
		spec.Name = name
		spec.Replay.Pieces[0] = expr.TextPiece(name + "(")
	}
	return spec, nil
}
