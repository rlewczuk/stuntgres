package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func (b *Binder) bindSelectedHstoreArray(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockHstore.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.functionSignatureError(raw)
	}
	arg := selected.args[0]
	if arg == nil {
		operand, err := b.comparisonOperand(raw.Args[0])
		if err != nil {
			return nil, err
		}
		arg, err = b.CoerceConditional(operand, selected.Inputs[0], "function")
		if err != nil {
			return nil, err
		}
	}
	installation, ok := view.Extension(procedure.Extension)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing hstore array installation")
	}
	member := extension.HstoreArrayProcedureSlot
	if symbol == "hstore_to_matrix" {
		member = extension.HstoreMatrixProcedureSlot
	}
	if installation.Members[member] != procedure.Address() {
		return nil, fmt.Errorf("stuntgres: invalid hstore array member")
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable}, arg.Properties())
	info := b.Info(b.Type(1009).Usage(), raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.HstoreArray{Info: info, Arg: arg, Formal: selected.Inputs[0].Type, Matrix: member == extension.HstoreMatrixProcedureSlot, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	return node, node.Validate()
}
