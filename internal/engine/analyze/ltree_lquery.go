package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedLtreeLquery(raw parser.FunctionExpr, selected sqlCallableSelection, operation sqltype.LtreeLqueryOperation) (expr.Expr, error) {
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
	member := extension.LtreeLqueryProcedureStart + int(operation)
	if selected, ok := extension.LtreeLqueryProcedure(view, procedure); !ok || selected != operation {
		return nil, fmt.Errorf("stuntgres: invalid ltree lquery member")
	}
	info := b.Info(b.Type(sqltype.ID(procedure.Result)).Usage(), raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.LtreeLquery{Info: info, Args: args, Formals: formals, Operation: operation, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	return node, node.Validate()
}
