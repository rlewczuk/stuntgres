package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Parser spelling is not semantic routine kind. Dynamic declarations, provider
// overrides and native Cube calls share the same current catalog selector.
// This runs before aggregate-only clause/recursive/partition gates, and never
// changes shared syntax or registers a scalar call in b.aggregates.
func (b *Binder) BindSQLRoutineSyntax(raw parser.Expr, policy ExpressionPolicy) (expr.Expr, bool, error) {
	if b.Clause.selectedCallReentry {
		b.Clause.selectedCallReentry = false
		return nil, false, nil
	}
	var call parser.FunctionExpr
	var cast *parser.CastExpr
	switch r := raw.(type) {
	case parser.AggregateExpr:
 if r.JSON != nil || r.Variadic || (!b.hasDynamicProcedureName(r.Name) && !b.hasProviderProcedureOverride(r.Name) && !functions.CubeFunctionName(r.Name) && !functions.EarthFunctionName(r.Name) && !extension.HstoreComparisonName(r.Name) && !extension.LtreePathProcedureName(r.Name) && !extension.LtreeLqueryProcedureName(r.Name) && !extension.LtreeLtxtqueryProcedureName(r.Name) && !extension.LtreeHierarchyProcedureName(r.Name) && !extension.LtreeArrayProcedureName(r.Name) && !extension.CubeComparisonName(r.Name) && !extension.CitextComparisonName(r.Name) && !extension.CitextPatternName(r.Name) && !extension.CitextRegexpName(r.Name) && !extension.CitextStringName(r.Name)) {
			return nil, false, nil
		}
		parts := []parser.Identifier{{Text: r.Name, Pos: r.Pos}}
		if r.Schema != nil {
			parts = []parser.Identifier{*r.Schema, parts[0]}
		}
		call = parser.FunctionExpr{Name: parser.ObjectName{Parts: parts}, Args: r.Args, Pos: r.Pos, Star: r.Star, Distinct: r.Distinct, Variadic: r.Variadic, Order: r.Order, Filter: r.Filter, Window: r.Window, Within: r.Within, WithinPos: r.WithinPos}
	case parser.CastExpr:
		if r.Syntax != parser.FunctionCast {
			return nil, false, nil
		}
		parts := []parser.Identifier{{Text: r.Callee, Pos: r.Pos}}
		if r.TypeName.Schema != nil {
			parts = []parser.Identifier{*r.TypeName.Schema, parts[0]}
		}
		call = parser.FunctionExpr{Name: parser.ObjectName{Parts: parts}, Args: []parser.Expr{r.Child}, Pos: r.Pos}
		cast = &r
	default:
		return nil, false, nil
	}
	if call.Within {
		call.Args = append([]parser.Expr(nil), call.Args...)
		for _, key := range call.Order {
			call.Args = append(call.Args, key.Expr)
		}
	}
	call = b.elideVoidParameters(call)
	selected, handled, err := b.selectSQLCallable(call, cast)
	if err != nil || !handled {
		return nil, handled, err
	}
	node, err := b.finishSQLCallable(call, selected, raw, policy)
	return node, true, err
}

func (b *Binder) sqlFunctionCastTarget(raw parser.TypeName, operand ComparisonOperand) (sqltype.Usage, sqltype.CoercionPlan, bool) {
	// An unresolved external Param does not take the unknown-literal shortcut.
	// Exact typed functions won already; literal/IO/relabel casts precede the
	// ordinary implicit/unknown best-match phase (PostgreSQL10.3 step3).
	unknownNull := false
	if literal, ok := operand.raw.(parser.LiteralExpr); ok {
		unknownNull = literal.Value.Kind == parser.NullValue
	}
	target, err := b.Host.ResolveAnnotationType(raw, unknownNull)
	if err != nil && operand.descriptor() != nil {
		target, err = b.resolveCastIdentity(raw, operand.descriptor())
	}
	if err != nil {
		return sqltype.Usage{}, sqltype.CoercionPlan{}, false
	}
	if literal, ok := operand.raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return target, sqltype.CoercionPlan{}, true
	}
	if operand.descriptor() == nil {
		if _, parameter := operand.raw.(parser.ParameterExpr); parameter {
			if metadata, ok := target.Type.PGTypeMetadata(); ok && metadata.Category == "S" {
				return target, sqltype.CoercionPlan{}, true
			}
		}
		return sqltype.Usage{}, sqltype.CoercionPlan{}, false
	}
	if recordCastPassthrough(operand.descriptor(), target.Type) {
		return operand.Node.ResultUsage(), sqltype.CoercionPlan{}, true
	}
	source := operand.Node.ResultUsage()
	if source.Type.BaseType() == target.Type.BaseType() {
		// Typename identity does not select a same-type sizing function.
		plan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
		return target, plan, err == nil && plan.FunctionCastShortcut()
	}
	declared, selectionErr := catalog.SelectCoercion(b.BindingCatalog(), source.BaseUsage(), target.BaseUsage(), sqltype.Explicit)
	if selectionErr != nil {
		return target, sqltype.CoercionPlan{}, false
	}
	if declared != nil {
		eligible := declared.Method == "b"
		if declared.Method == "i" {
			metadata, _ := target.Type.BaseType().PGTypeMetadata()
			eligible = !source.Type.BaseType().IsComposite() || metadata.Category != "S"
		}
		// The selected coercion owner constructs declared binary/IO nodes;
		// a generic sqltype plan is neither their authority nor their recipe.
		return target, sqltype.CoercionPlan{}, eligible
	}
	plan, err := sqltype.PlanCoercion(operand.Node.ResultUsage(), target, sqltype.Explicit)
	return target, plan, err == nil && plan.FunctionCastShortcut()
}

// Retain only the selected type identity, never an executable expression or a
// coercion plan. Existing analysis invalidation retires this occurrence group.
func (b *Binder) retainedTypeNameCast(raw parser.FunctionExpr) (expr.Expr, bool, error) {
	if !b.Expected.Held {
		return nil, false, nil
	}
	key := PreparedTypeKey{Offset: raw.Pos.Offset, Kind: TypeNameCast}
	captured, found := b.Expected.Types[key]
	if !found {
		return nil, false, nil
	}
	view := b.BindingCatalog()
	if b.Expected.CatalogOwner != view.Owner() || b.Expected.CatalogEpoch != view.Epoch() {
		return nil, false, nil
	}
	if len(raw.Name.Parts) == 0 || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Star || raw.Variadic {
		return nil, true, fmt.Errorf("stuntgres: incompatible retained typename occurrence")
	}
	actual, ok := view.LookupType(captured.Base.Type.ID())
	compatible := ok && (actual == captured.Base.Type || captured.Base.Type.Concrete() && (captured.Base.Type.ID() == 2249 || captured.Base.Type.ID() == 2287)) || captured.Base.Type.IsDeferredRecord()
	annotation := parser.TypeName{Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text, Quoted: true, Pos: raw.Pos}
	if len(raw.Name.Parts) == 2 {
		annotation.Schema = &raw.Name.Parts[0]
	}
	resolved, resolveErr := b.resolveCastIdentity(annotation, captured.Base.Type)
	compatible = compatible && resolveErr == nil && (resolved.Type.ID() == captured.Base.Type.ID() || recordCastPassthrough(captured.Base.Type, resolved.Type))
	if !compatible || captured.Kind != TypeNameCast {
		return nil, true, fmt.Errorf("stuntgres: incompatible retained typename authority")
	}
	// Still resolve the current lexical namespace under ordinary authorization;
	// only the selected semantic target survives a preexisting overload shadow.
	auth, err := b.BindingAuthorization()
	if err != nil {
		return nil, true, err
	}
	if _, err := b.Host.LookupNamespaces(auth, raw.Name, FunctionLookup); err != nil {
		return nil, true, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, true, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, true, err
	}
	operand, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, true, err
	}
	target, coercion, eligible := b.sqlFunctionCastTarget(annotation, operand)
	if !eligible {
		name := "unknown"
		if typ := operand.descriptor(); typ != nil {
			name = b.DiagnosticTypeName(typ)
		}
		return nil, true, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, []string{name}, false)
	}
	node, err := b.finishSQLCallable(raw, sqlCallableSelection{typeNameTarget: target, typeNamePlan: coercion, typeNameOperand: operand}, nil, ExpressionPolicy{Context: ProjectionContext})
	return node, true, err
}

func (b *Binder) typeNameCallSchema(target *sqltype.Descriptor) (string, error) {
	if !target.IsCube() {
		return target.NamespaceName(), nil
	}
	actual, ok := b.BindingCatalog().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
	if !ok || actual.SQLType != target {
		return "", fmt.Errorf("stuntgres: missing cube type-call authority")
	}
	namespace, ok := b.BindingCatalog().Namespace(actual.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing cube type-call namespace")
	}
	return namespace.Name(), nil
}
