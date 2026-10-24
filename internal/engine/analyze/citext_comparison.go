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

func (b *Binder) bindSelectedCitextComparison(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	p := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockCitext.ValidateProcedure(view, p); err != nil {
		return nil, err
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
	properties := expr.Properties{Volatility: expr.Immutable}
	for i := range args {
		args[i] = selected.args[i]
		if args[i] == nil {
			operand, err := b.comparisonOperand(raw.Args[i])
			if err != nil {
				return nil, err
			}
			args[i], err = b.CoerceConditional(operand, selected.Inputs[i], "function")
			if err != nil {
				return nil, err
			}
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	kind := uint8(len(extension.CitextComparisonNames))
	for i, name := range extension.CitextComparisonNames {
		if name == symbol {
			kind = uint8(i)
			break
		}
	}
	if kind >= 6 {
		return nil, fmt.Errorf("stuntgres: invalid citext comparison operation")
	}
	input, err := b.comparisonInfo(args[0], args[1])
	if err != nil {
		return nil, err
	}
	result := b.Type(sqltype.Bool)
	comparison, err := sqltype.BindCitextComparison(sqltype.ComparisonOperator(kind), args[0].ResultUsage().Type, args[1].ResultUsage().Type, result, input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, err
	}
	info := b.Info(result.Usage(), raw.Pos, properties)
	info.RoutineReference = p
	ref := view.Owner().Domain.Ref(view.Epoch(), p.Address())
	b.dependency(ref, p.Revision())
	return plan.CitextComparisonExpr{Info: info, Ref: ref, Args: args, Comparison: comparison, Kind: kind, Namespace: view.Get(p.Namespace()).Name(), Collation: input.Usage.Collation, Indeterminate: input.CollationConflict}, nil
}
