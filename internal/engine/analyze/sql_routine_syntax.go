package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Parser spelling is not semantic routine kind. Resolve only names that have a
// real SQL declaration; all negative paths retain existing aggregate/cast owners.
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
		if r.JSON != nil || r.Within || r.Variadic || !b.hasDynamicProcedureName(r.Name) && !functions.CubeFunctionName(r.Name) {
			return nil, false, nil
		}
		parts := []parser.Identifier{{Text: r.Name, Pos: r.Pos}}
		if r.Schema != nil {
			parts = []parser.Identifier{*r.Schema, parts[0]}
		}
		call = parser.FunctionExpr{Name: parser.ObjectName{Parts: parts}, Args: r.Args, Pos: r.Pos, Star: r.Star, Distinct: r.Distinct, Variadic: r.Variadic, Order: r.Order, Filter: r.Filter, Window: r.Window, Within: r.Within, WithinPos: r.WithinPos}
	case parser.CastExpr:
		if r.Syntax != parser.FunctionCast || !b.hasDynamicProcedureName(r.Callee) && !functions.CubeFunctionName(r.Callee) {
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
	target, err := b.Host.ResolveAnnotationType(raw, false)
	if err != nil {
		return sqltype.Usage{}, sqltype.CoercionPlan{}, false
	}
	if literal, ok := operand.raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return target, sqltype.CoercionPlan{}, true
	}
	if operand.descriptor() == nil {
		return sqltype.Usage{}, sqltype.CoercionPlan{}, false
	}
	plan, err := sqltype.PlanCoercion(operand.Node.ResultUsage(), target, sqltype.Explicit)
	return target, plan, err == nil && plan.FunctionCastShortcut()
}

func geometricTypeNameID(name string) (sqltype.ID, bool) {
	switch name {
	case "box":
		return sqltype.Box, true
	case "circle":
		return sqltype.Circle, true
	case "lseg":
		return sqltype.Lseg, true
	case "path":
		return sqltype.Path, true
	case "point":
		return sqltype.Point, true
	case "polygon":
		return sqltype.Polygon, true
	}
	return 0, false
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	id, geometric := geometricTypeNameID(name)
	compatible := geometric && captured.Base == b.Type(id).Usage()
	if captured.Base.Type.IsCube() && name == "cube" {
		actual, ok := view.LookupType(captured.Base.Type.ID())
		compatible = ok && actual == captured.Base.Type
	}
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
	target := captured.Base.Type
	schema, err := b.typeNameCallSchema(target)
	if err != nil {
		return nil, true, err
	}
	cast := parser.CastExpr{
		Child:    raw.Args[0],
		TypeName: parser.TypeName{Name: target.Name(), Schema: &parser.Identifier{Text: schema}, Pos: raw.Pos},
		Syntax:   parser.StandardCast, Pos: raw.Pos,
	}
	node, err := b.Cast(cast, ExpressionPolicy{Context: ProjectionContext})
	if err == nil {
		b.Prepared.CaptureComposite(key, captured.Base, nil)
	}
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

func legacyFunctionCastPredicate(raw parser.Expr) bool {
	switch child := raw.(type) {
	case parser.PredicateExpr, parser.LogicalExpr, parser.InListExpr, parser.ArrayComparisonExpr:
		return true
	case parser.OperatorExpr:
		if len(child.Name.Parts) == 1 {
			switch child.Name.Parts[0].Text {
			case "=", "<", ">", "<=", ">=", "<>":
				return true
			}
		}
	}
	return false
}
