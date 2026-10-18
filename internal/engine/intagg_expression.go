package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// resolveIntaggAggregate analyzes an int_array_aggregate call's operands, its
// FILTER and its installed routine, and records the routine's identity.
func (b *expressionBinder) resolveIntaggAggregate(raw parser.AggregateExpr, call parser.FunctionExpr) (BoundExpr, *procedureDescriptor, ids.ObjectRef, BoundExpr, error) {
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

func (b *expressionBinder) bindIntaggAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	if b.aggregateClause == "INSERT VALUES" && b.queryNamespace == nil {
		// As with SUM/bitwise aggregates, VALUES has no input namespace.
		// Destination columns are inaccessible diagnostic candidates only.
		b.queryNamespace = &queryNamespace{}
		hidden := b.queryHidden
		if target := b.insertTarget; target != nil {
			columns := make([]queryColumn, len(target.columns))
			for i, descriptor := range target.columns {
				columns[i].descriptor = descriptor
			}
			b.queryHidden = append(b.queryHidden, queryQualifier{name: target.name, columns: columns, lateralForbidden: true})
		}
		defer func() { b.queryNamespace, b.queryHidden = nil, hidden }()
	}
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
	call := aggregateCall{provider: functions.NewArrayAggregateProvider(arg.ResultUsage().Type, array.Usage(), false), argument: arg, operation: aggregateArrayScalar, includeNulls: true, procedure: proc, ref: ref, routine: ref}
	return b.finishAggregate(raw, call, filter, info)
}

func sameIntaggIdentity(a *procedureDescriptor, ar ids.ObjectRef, b *procedureDescriptor, br ids.ObjectRef) bool {
	if ar != br || (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.address == b.address && a.revision == b.revision
}

func validateIntaggIdentity(v CatalogRead, proc *procedureDescriptor, ref ids.ObjectRef, name string) error {
	if proc == nil {
		if ref != (ids.ObjectRef{}) {
			return fmt.Errorf("stuntgres: missing installed intagg identity")
		}
		return nil
	}
	actual, ok := v.state.descriptors[ref.Address].(*procedureDescriptor)
	if !ok || actual != proc || ref.Domain() != v.owner.catalog.domain || ref.Epoch != v.Epoch() || proc.name != name || proc.intagg != stockIntagg {
		return fmt.Errorf("stuntgres: invalid stored intagg identity")
	}
	return stockIntagg.validateProcedure(v, proc)
}

// intaggIdentity records the installed intagg routine as a dependency, as
// the engine binder always appended it, and returns its reference.
func (r installedExtensionRoutine) intaggIdentity() (*procedureDescriptor, ids.ObjectRef, error) {
	p := r.procedure
	if p.intagg != stockIntagg {
		return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid installed intagg routine")
	}
	ref := r.binder.engine.catalog.domain.Ref(r.catalog.Epoch(), p.address)
	r.binder.dependencies = append(r.binder.dependencies, expressionDependency{ref: ref, revision: p.revision})
	return p, ref, nil
}

// attachIntaggEnumeration admits int_array_enum where a set-returning call
// may appear and lifts it as the engine's set-returning unnest node.
func (r installedExtensionRoutine) attachIntaggEnumeration(call functions.IntaggEnumeration) (BoundExpr, error) {
	b := r.binder
	if b.executeParameters {
		return nil, expressionError("0A000", "set-returning functions are not allowed in EXECUTE parameters", call.Pos)
	}
	if b.policyConsumer && b.queryFrame == 0 {
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
	return unnestExpression{expressionInfo: call.Info, args: []BoundExpr{call.Arg}, procedure: proc, ref: ref}, nil
}
