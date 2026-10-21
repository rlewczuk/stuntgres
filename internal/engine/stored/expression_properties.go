package stored

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// The canonical mapper adds every mapped child's properties to this base before
// constructing its existing node. Preserve analyzed dependence from eliminated
// branches and runtime domain barriers, but do not retain replaced child demand.
func RewrittenIntrinsicProperties(node expr.Expr) expr.Properties {
	var children expr.Properties
	plan.ExpressionChildren(node, func(child expr.Expr) {
		if child != nil {
			children = expr.MergeProperties(children, child.Properties())
		}
	})
	original := node.Properties()
	properties := expr.Properties{Volatility: PlannedIntrinsicVolatility(node)}
	if original.Volatility > children.Volatility {
		properties.Volatility = max(properties.Volatility, original.Volatility)
	}
	properties.RowDependent = original.RowDependent && !children.RowDependent
	properties.StageDependent = original.StageDependent && !children.StageDependent
	properties.ParameterDependent = original.ParameterDependent && !children.ParameterDependent
	properties.DomainDependent = original.DomainDependent && !children.DomainDependent
	properties.RuntimeDomainConversion = original.RuntimeDomainConversion
	switch node.(type) {
	case plan.ColumnExpr, plan.ReturningColumnExpr, plan.ReturningWholeRowExpr, plan.TableOIDExpr, plan.GroupExpr, plan.AggregateExpr, plan.GroupingExpr, *plan.WindowExpr, plan.CachedOrderExpr:
		properties.RowDependent = original.RowDependent || properties.RowDependent
		properties.StageDependent = original.StageDependent || properties.StageDependent
	case plan.SubqueryExpr:
		// Its owned query is not a lexical child; query mapping adds fresh demand.
		properties = expr.MergeProperties(properties, original)
	}
	return properties
}
