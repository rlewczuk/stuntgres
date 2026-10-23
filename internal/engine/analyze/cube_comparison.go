package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedCubeComparison(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	p := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockCube.ValidateProcedure(view, p); err != nil {
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
	kind := uint8(len(extension.CubeComparisonNames))
	for i, name := range extension.CubeComparisonNames {
		if name == symbol {
			kind = uint8(i)
			break
		}
	}
	if int(kind) == len(extension.CubeComparisonNames) {
		return nil, fmt.Errorf("stuntgres: invalid installed cube comparison operation")
	}
	result := b.Type(sqltype.ID(p.Result))
	info := b.Info(result.Usage(), raw.Pos, properties)
	info.RoutineReference = p
	ref := view.Owner().Domain.Ref(view.Epoch(), p.Address())
	b.dependency(ref, p.Revision())
	comparison, err := sqltype.BindComparison(args[0].ResultUsage().Type, args[1].ResultUsage().Type, sqltype.CollationRef{}, false)
	if err != nil {
		return nil, err
	}
	namespace := view.Get(p.Namespace()).Name()
	return plan.CubeComparisonExpr{Info: info, Ref: ref, Args: args, Comparison: comparison, Kind: kind, Namespace: namespace}, nil
}

// Ordering selects the current installed default class and its real support
// routine before reading rows. The static builtin class registry is untouched.
func (b *Binder) bindCubeOrdering(usage sqltype.Usage) (sqltype.Comparison, error) {
	view := b.BindingCatalog()
	class, ok := view.DefaultIndexClass(403, usage.Type)
	if !ok {
		return sqltype.Comparison{}, fmt.Errorf("stuntgres: missing cube ordering class")
	}
	comparison, err := routine.BindIndexOrdering(view, usage, class)
	if err != nil {
		return sqltype.Comparison{}, err
	}
	fact, ok := view.OperatorClass(ids.OID(class.OID()))
	if !ok {
		return sqltype.Comparison{}, fmt.Errorf("stuntgres: missing cube ordering descriptor")
	}
	for _, address := range []ids.ObjectAddress{fact.Address(), {ClassID: ids.OperatorFamilyClassID, ObjectID: fact.Family}, {ClassID: ids.AccessMethodProcedureClassID, ObjectID: ids.OID(comparison.Support().Member)}, {ClassID: ids.ProcedureClassID, ObjectID: ids.OID(comparison.Support().Routine)}} {
		d := view.Get(address)
		if d == nil {
			return sqltype.Comparison{}, fmt.Errorf("stuntgres: missing cube ordering dependency")
		}
		b.dependency(view.Owner().Domain.Ref(view.Epoch(), address), d.Revision())
	}
	return comparison.DatumComparison(), nil
}

func (b *Binder) bindOrdering(usage sqltype.Usage) (sqltype.Comparison, error) {
	if usage.Type.BaseType().IsCube() {
		return b.bindCubeOrdering(usage)
	}
	return sqltype.BindOrdering(usage)
}
