package analyze

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedLtreeHierarchy(raw parser.FunctionExpr, selected sqlCallableSelection, operation sqltype.LtreeHierarchyOperation) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockLtree.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if len(raw.Args) != 2 || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.functionSignatureError(raw)
	}
	var args [2]expr.Expr
	var formals [2]*sqltype.Descriptor
	properties := expr.Properties{Volatility: expr.Immutable}
	for i := range 2 {
		arg := selected.args[i]
		if arg == nil {
			operand, err := b.comparisonOperand(raw.Args[i])
			if err != nil {
				return nil, err
			}
			arg, err = b.CoerceConditional(operand, selected.Inputs[i], "function")
			if err != nil {
				return nil, err
			}
		}
		args[i], formals[i] = arg, selected.Inputs[i].Type
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	installation, ok := catalog.OriginInstallation(procedure)
	member := extension.LtreeHierarchyProcedureStart + int(operation)
	if !ok || procedure.Origin.Declaration != member || member >= len(installation.Members) || installation.Members[member] != procedure.Address() {
		return nil, fmt.Errorf("stuntgres: invalid ltree hierarchy member")
	}
	info := b.Info(b.Type(sqltype.ID(procedure.Result)).Usage(), raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.LtreeHierarchy{Info: info, Args: args, Formals: formals, Operation: operation, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	return node, node.Validate()
}
