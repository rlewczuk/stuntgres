package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// analyzeIntaggArguments analyzes intagg call operands, then checks the
// argument limit and labels.
func analyzeIntaggArguments(b functions.Binder, raw parser.FunctionExpr) ([]functions.Argument, error) {
	var small [1]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= 1 {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
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
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := functions.CallLabelError(raw); err != nil {
		return nil, err
	}
	return operands, nil
}

// resolveIntagg selects the installed intagg routine for one operand:
// int_array_enum(integer[]) or int_array_aggregate(integer). Search path
// errors precede signature errors; the operand is coerced to the formal.
func resolveIntagg(b functions.Binder, raw parser.FunctionExpr, operands []functions.Argument) (expr.Expr, functions.InstalledRoutine, error) {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = functions.NamedArgumentType(raw, i, names[i])
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	formal, signature := b.Type(sqltype.Int4).Usage(), "23"
	if strings.EqualFold(name, "int_array_enum") {
		formal, signature = sqltype.Usage{}, "1007"
	}
	routine, err := b.InstalledRoutine(raw.Name, functions.IntaggExtension, signature)
	if err != nil {
		return nil, nil, err
	}
	if formal.Type == nil {
		array, ok := b.ArrayType(b.Type(sqltype.Int4))
		if !ok {
			return nil, nil, fmt.Errorf("stuntgres: missing integer array descriptor")
		}
		formal = array.Usage()
	}
	if raw.Star || len(operands) != 1 || len(raw.ArgNames) != 0 {
		return nil, nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if operands[0].Usage.Type != nil {
		if _, err := sqltype.PlanCoercion(operands[0].Usage, formal, sqltype.Implicit); err != nil {
			return nil, nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	if routine == nil {
		return nil, nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil {
		return nil, nil, err
	}
	arg, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.DirectFormalArgument, Target: formal})
	if err != nil {
		return nil, nil, err
	}
	return arg, routine, nil
}

// resolveIntaggAggregate analyzes an int_array_aggregate call's operands, its
// FILTER and its installed routine, and records the routine's identity.
func (b *expressionBinder) resolveIntaggAggregate(raw parser.AggregateExpr, call parser.FunctionExpr) (BoundExpr, *procedureDescriptor, ids.ObjectRef, BoundExpr, error) {
	operands, err := analyzeIntaggArguments(b, call)
	if err != nil {
		return nil, nil, ids.ObjectRef{}, nil, err
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, nil, ids.ObjectRef{}, nil, err
	}
	arg, routine, err := resolveIntagg(b, call, operands)
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

// bindIntagg binds intagg's installed routines called as functions. Only
// int_array_enum is one; int_array_aggregate called this way is missing.
func bindIntagg(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands, err := analyzeIntaggArguments(b, raw)
	if err != nil {
		return nil, err
	}
	arg, routine, err := resolveIntagg(b, raw, operands)
	if err != nil {
		return nil, err
	}
	// The installed routine's name is the written name, found exactly.
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "int_array_enum" {
		return nil, b.SignatureError(raw.Name, raw.Pos, []string{b.DiagnosticTypeName(arg.ResultUsage().Type)})
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable, StageDependent: true}, arg.Properties())
	info := b.Info(b.Type(sqltype.Int4).Usage(), raw.Pos, properties)
	return routine.Attach(functions.IntaggEnumeration{Info: info, Arg: arg})
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
