package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// HashFilterProbe describes ordinary same-usage equality without evaluating its
// literal/parameter probe. Metadata planning and live-root admission share it.
func HashFilterProbe(n *FilterNode, ref ids.ObjectRef, scope, frame uint32) (ColumnExpr, expr.Expr, bool) {
	predicate, ok := n.Predicate.(ComparisonExpr)
	if !ok || predicate.Operator != "=" || predicate.Namespace != "" && predicate.Namespace != "pg_catalog" { return ColumnExpr{}, nil, false }
	column, ok := predicate.Left.(ColumnExpr)
	probe := predicate.Right
	if !ok { column, ok = predicate.Right.(ColumnExpr); probe = predicate.Left }
	if !ok || column.Scope != scope || column.Frame != frame || column.Origin.Domain() != ref.Domain() || column.Origin.Epoch != ref.Epoch || column.Origin.Address.ClassID != ref.Address.ClassID || column.Origin.Address.ObjectID != ref.Address.ObjectID { return ColumnExpr{}, nil, false }
	switch probe.(type) { case LiteralExpr, ParameterExpr: default: return ColumnExpr{}, nil, false }
	if probe.ResultUsage() != column.ResultUsage() || !predicate.Predicate.EqualityFor(column.ResultUsage()) { return ColumnExpr{}, nil, false }
	return column, probe, true
}
