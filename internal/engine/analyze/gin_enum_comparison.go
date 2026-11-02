package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedGinEnumComparison(raw parser.FunctionExpr, selected sqlCallableSelection) (expr.Expr, error) {
	p := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockBtreeGin.ValidateProcedure(view, p); err != nil {
		return nil, err
	}
	symbol, err := extension.ProcedureSymbol(view, p)
	if err != nil {
		return nil, err
	}
	if symbol != "gin_enum_cmp" {
		return nil, fmt.Errorf("stuntgres: internal GIN support is not a scalar comparator")
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if len(raw.Args) != 2 || len(selected.Inputs) != 2 || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.functionSignatureError(raw)
	}
	var args [2]expr.Expr
	properties := selected.properties
	for i := range args {
		args[i] = selected.args[i]
		if args[i] == nil {
			operand, err := b.comparisonOperand(raw.Args[i])
			if err != nil {
				return nil, err
			}
			target := selected.Inputs[i]
			if selected.Targets != nil {
				target = selected.Targets[i]
			}
			if target.Type == nil {
				return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
			}
			args[i], err = b.CoerceConditional(operand, target, "function")
			if err != nil {
				return nil, err
			}
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	left, right := args[0].ResultUsage().Type, args[1].ResultUsage().Type
	if left == nil || !left.BaseType().IsEnum() || left != right {
		return nil, b.functionSignatureError(raw)
	}
	comparison, err := sqltype.BindComparison(left, right, sqltype.CollationRef{}, false)
	if err != nil {
		return nil, err
	}
	info := b.Info(b.Type(sqltype.Int4).Usage(), raw.Pos, properties)
	info.RoutineReference = p
	ref := view.Owner().Domain.Ref(view.Epoch(), p.Address())
	b.dependency(ref, p.Revision())
	return plan.GinEnumCompareExpr{Info: info, Ref: ref, Args: args, Comparison: comparison, Namespace: view.Get(p.Namespace()).Name()}, nil
}
