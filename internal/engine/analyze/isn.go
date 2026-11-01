package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindIsnRoutine(d *catalog.Procedure, args []expr.Expr, pos parser.Position, named bool) (expr.Expr, error) {
	view := b.BindingCatalog()
	if err := extension.StockIsn.ValidateProcedure(view, d); err != nil {
		return nil, err
	}
	for _, arg := range args {
		if arg == nil {
			return nil, fmt.Errorf("stuntgres: missing ISN operand")
		}
		typ, ok := view.LookupType(arg.ResultUsage().Type.ID())
		if !ok || typ != arg.ResultUsage().Type {
			return nil, fmt.Errorf("stuntgres: foreign ISN operand generation")
		}
	}
	ref := b.Host.CatalogOwner().Domain.Ref(view.Epoch(), d.Address())
	b.dependency(ref, d.Revision())
	if operation, ok := extension.IsnValidationOperation(view, d); ok {
		result, exists := view.LookupType(sqltype.ID(d.Result))
		if !exists {
			return nil, fmt.Errorf("stuntgres: missing ISN validation result")
		}
		properties := expr.Properties{Volatility: d.Volatility}
		for _, arg := range args {
			properties = expr.MergeProperties(properties, arg.Properties())
		}
		info := b.Info(result.Usage(), pos, properties)
		info.RoutineReference = d
		return functions.NewIsnValidation(info, operation, d.Name(), view.Get(d.Namespace()).Name(), ref, args)
	}
	if op, ok := extension.IsnComparisonOperation(view, d); ok {
		if len(args) != 2 {
			return nil, fmt.Errorf("stuntgres: invalid ISN comparison arity")
		}
		if d.Signature != catalog.ProcedureSignature(fmt.Sprintf("%d,%d", args[0].ResultUsage().Type.ID(), args[1].ResultUsage().Type.ID())) {
			return nil, fmt.Errorf("stuntgres: mismatched ISN comparison formals")
		}
		predicate, err := sqltype.BindComparisonPredicate(op, args[0].ResultUsage().Type, args[1].ResultUsage().Type, b.Type(sqltype.Bool), sqltype.CollationRef{}, false)
		if err != nil {
			return nil, err
		}
		info := b.Info(b.Type(sqltype.Bool).Usage(), pos, expr.MergeProperties(args[0].Properties(), args[1].Properties()))
		info.RoutineReference = d
		return plan.ComparisonExpr{Info: info, RoutineRef: ref, Left: args[0], Right: args[1], Predicate: predicate, Operator: []string{"=", "<>", "<", "<=", ">", ">="}[op], Namespace: view.Get(d.Namespace()).Name(), Named: named}, nil
	}
	slot := d.Origin.Declaration
	if slot >= extension.IsnConversionStart && slot < extension.IsnSupportStart {
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid ISN conversion arity")
		}
		if d.Signature != extension.IsnSignature(args[0].ResultUsage().Type.ID()) {
			return nil, fmt.Errorf("stuntgres: mismatched ISN conversion formal")
		}
		result, ok := view.LookupType(sqltype.ID(d.Result))
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing ISN conversion result")
		}
		if _, err := catalog.SelectCoercion(view, args[0].ResultUsage(), result.Usage(), sqltype.Explicit); err != nil {
			return nil, err
		}
		info := b.Info(result.Usage(), pos, args[0].Properties())
		info.RoutineReference = d
		return functions.NewCastRoutine(info, d.Name(), view.Get(d.Namespace()).Name(), args, d.Volatility)
	}
	return nil, fmt.Errorf("stuntgres: ISN routine has no selected scalar provider")
}

func (b *Binder) bindSelectedIsn(raw parser.FunctionExpr, selected sqlCallableSelection) (expr.Expr, error) {
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	args := make([]expr.Expr, len(selected.Inputs))
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
	}
	return b.bindIsnRoutine(selected.Procedure, args, raw.Pos, true)
}

func (b *Binder) isnOperatorProcedure(name string, left, right *sqltype.Descriptor) (*catalog.Procedure, error) {
	view := b.BindingCatalog()
	from, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(left.ID())})
	to, rightOK := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(right.ID())})
	if !ok || !rightOK || from.SQLType != left || to.SQLType != right {
		return nil, fmt.Errorf("stuntgres: foreign ISN comparison operands")
	}
	if err := extension.StockIsn.ValidateISNConversion(view, from, to); err != nil {
		return nil, err
	}
	installation := from.Origin.Installation
	for slot, canonical := range installation.Declarations {
		o, ok := canonical.(*catalog.Operator)
		if !ok || o.Name() != name || o.Left != ids.OID(left.ID()) || o.Right != ids.OID(right.ID()) {
			continue
		}
		current, ok := view.Operator(installation.Members[slot])
		if !ok {
			break
		}
		if err := catalog.ValidateInstalledClassOrigin(view, current); err != nil {
			return nil, err
		}
		proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: current.Function})
		if !ok {
			break
		}
		if err := extension.StockIsn.ValidateProcedure(view, proc); err != nil {
			return nil, err
		}
		for _, d := range []catalog.Descriptor{current, proc} {
			b.dependency(view.Owner().Domain.Ref(view.Epoch(), d.Address()), d.Revision())
		}
		return proc, nil
	}
	return nil, fmt.Errorf("stuntgres: missing installed ISN comparison")
}
