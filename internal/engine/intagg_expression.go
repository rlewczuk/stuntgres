package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) intaggArguments(raw parser.FunctionExpr) ([]functions.Argument[BoundExpr], error) {
	var small [1]functions.Argument[BoundExpr]
	var operands []functions.Argument[BoundExpr]
	if len(raw.Args) <= 1 {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	if issue.Malformed {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	if issue.Positional {
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	}
	if issue.Duplicate != "" {
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	return operands, nil
}

func (b *expressionBinder) resolveIntagg(raw parser.FunctionExpr, operands []functions.Argument[BoundExpr]) (BoundExpr, *procedureDescriptor, ObjectRef, error) {
	var small [1]string
	var names []string
	if len(operands) <= 1 {
		names = small[:len(operands)]
	} else {
		names = make([]string, len(operands))
	}
	for i, o := range operands {
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.diagnosticTypeName(o.Usage.Type)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	failure := func() (BoundExpr, *procedureDescriptor, ObjectRef, error) {
		return nil, nil, ObjectRef{}, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, nil, ObjectRef{}, err
	}
	path, err := b.engine.lookupNamespacesSnapshot(auth, raw.Name, installedFunctionLookup)
	if err != nil {
		return nil, nil, ObjectRef{}, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	formal := b.typ(sqltype.Int4).Usage()
	signature := procedureSignature("23")
	if strings.EqualFold(name, "int_array_enum") {
		array, ok := b.definitionArray(b.typ(sqltype.Int4))
		if !ok {
			return nil, nil, ObjectRef{}, fmt.Errorf("stuntgres: missing integer array descriptor")
		}
		formal, signature = array.Usage(), "1007"
	}
	if raw.Star || len(operands) != 1 || len(raw.ArgNames) != 0 {
		return failure()
	}
	if operands[0].Usage.Type != nil {
		if _, err := sqltype.PlanCoercion(operands[0].Usage, formal, sqltype.Implicit); err != nil {
			return failure()
		}
	}
	proc := procedureInNamespaces(auth.catalog, path, name, signature)
	if proc == nil || proc.intagg != stockIntagg {
		return failure()
	}
	if err := stockIntagg.validateProcedure(auth.catalog, proc); err != nil {
		return nil, nil, ObjectRef{}, err
	}
	arg, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.DirectFormalArgument, Target: formal})
	if err != nil {
		return nil, nil, ObjectRef{}, err
	}
	ref := ObjectRef{owner: b.engine.domain, Epoch: auth.catalog.Epoch(), Address: proc.address}
	b.dependencies = append(b.dependencies, expressionDependency{ref: ref, revision: proc.revision})
	return arg, proc, ref, nil
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
	operands, err := b.intaggArguments(callRaw)
	if err != nil {
		return nil, err
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, proc, ref, err := b.resolveIntagg(callRaw, operands)
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

func (b *expressionBinder) bindIntaggFunction(raw parser.FunctionExpr) (BoundExpr, error) {
	operands, err := b.intaggArguments(raw)
	if err != nil {
		return nil, err
	}
	arg, proc, ref, err := b.resolveIntagg(raw, operands)
	if err != nil {
		return nil, err
	}
	if proc.name != "int_array_enum" {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, []string{b.diagnosticTypeName(arg.ResultUsage().Type)})
	}
	if err := rangeInspectDecorationError(raw); err != nil {
		return nil, err
	}
	if b.executeParameters {
		return nil, expressionError("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos)
	}
	if b.policyConsumer && b.queryFrame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
	}
	properties := mergeExpressionProperties(ExprProperties{Volatility: Immutable, StageDependent: true}, arg.Properties())
	info := b.info(b.typ(sqltype.Int4).Usage(), raw.Pos, properties)
	info.routineReference = proc
	return unnestExpression{expressionInfo: info, args: []BoundExpr{arg}, procedure: proc, ref: ref}, nil
}

func sameIntaggIdentity(a *procedureDescriptor, ar ObjectRef, b *procedureDescriptor, br ObjectRef) bool {
	if ar != br || (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.address == b.address && a.revision == b.revision
}

func validateIntaggIdentity(v CatalogRead, proc *procedureDescriptor, ref ObjectRef, name string) error {
	if proc == nil {
		if ref != (ObjectRef{}) {
			return fmt.Errorf("stuntgres: missing installed intagg identity")
		}
		return nil
	}
	actual, ok := v.state.descriptors[ref.Address].(*procedureDescriptor)
	if !ok || actual != proc || ref.owner != v.owner.domain || ref.Epoch != v.Epoch() || proc.name != name || proc.intagg != stockIntagg {
		return fmt.Errorf("stuntgres: invalid stored intagg identity")
	}
	return stockIntagg.validateProcedure(v, proc)
}
