package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// resolveIntaggAggregate analyzes an int_array_aggregate call's operands, its
// FILTER and its installed routine, and records the routine's identity.
func (b *expressionBinder) resolveIntaggAggregate(raw parser.AggregateExpr, call parser.FunctionExpr) (expr.Expr, *catalog.Procedure, ids.ObjectRef, expr.Expr, error) {
	operands, err := functions.AnalyzeIntaggArguments(b, call)
	if err != nil {
		return nil, nil, ids.ObjectRef{}, nil, err
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, nil, ids.ObjectRef{}, nil, err
	}
	arg, routine, err := functions.ResolveIntagg(b, call, operands)
	if err != nil {
		return nil, nil, ids.ObjectRef{}, nil, err
	}
	installed, ok := routine.(installedExtensionRoutine)
	if !ok {
		return nil, nil, ids.ObjectRef{}, nil, fmt.Errorf("stuntgres: invalid installed intagg routine")
	}
	proc, ref, err := installed.intaggIdentity()
	if err != nil {
		return nil, nil, ids.ObjectRef{}, nil, err
	}
	return arg, proc, ref, filter, nil
}

func (b *expressionBinder) bindIntaggAggregate(raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	// As with SUM/bitwise aggregates, VALUES has no input namespace.
	defer b.valuesAggregateNamespace()()
	callRaw := parser.FunctionExpr{Name: name, Args: raw.Args, Star: raw.Star, Variadic: raw.Variadic, Pos: raw.Pos}
	arg, proc, ref, filter, err := b.resolveIntaggAggregate(raw, callRaw)
	if err != nil {
		return nil, err
	}
	array, ok := b.definitionArray(b.typ(sqltype.Int4))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing integer array descriptor")
	}
	properties := arg.Properties()
	properties.StageDependent = true
	info := b.info(array.Usage(), raw.Pos, properties)
	call := aggregateCall{Provider: functions.NewArrayAggregateProvider(arg.ResultUsage().Type, array.Usage(), false), Argument: arg, Operation: functions.AggregateArrayScalar, IncludeNulls: true, Procedure: proc, Ref: ref, Routine: ref}
	return b.finishAggregate(raw, call, filter, info)
}

// intaggIdentity records the installed intagg routine as a dependency, as
// the engine binder always appended it, and returns its reference.
func (r installedExtensionRoutine) intaggIdentity() (*catalog.Procedure, ids.ObjectRef, error) {
	p := r.procedure
	if intaggProviderOf(p) != stockIntagg {
		return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid installed intagg routine")
	}
	ref := r.binder.engine.catalog.Domain.Ref(r.catalog.Epoch(), p.Address())
	r.binder.dependencies = append(r.binder.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: p.Revision()})
	return p, ref, nil
}

// attachIntaggEnumeration admits int_array_enum where a set-returning call
// may appear and lifts it as the engine's set-returning unnest node.
func (r installedExtensionRoutine) attachIntaggEnumeration(call functions.IntaggEnumeration) (expr.Expr, error) {
	b := r.binder
	if b.params.executeParameters {
		return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", call.Pos)
	}
	if b.consumer.policyConsumer && b.query.frame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if err := b.AdmitSetReturning(call.Pos); err != nil {
		return nil, err
	}
	proc, ref, err := r.intaggIdentity()
	if err != nil {
		return nil, err
	}
	call.RoutineReference = proc
	return unnestExpression{Info: call.Info, Args: []expr.Expr{call.Arg}, Procedure: proc, Ref: ref}, nil
}
