package analyze

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// Inspect selected SUM/bitwise/JSON scalar composition without crossing a child query's frame.
// Only failed analysis (nil bound source) uses this check; assignment failures
// with an actual source retain the destination's ordinary diagnostic.
func ContainsValuesAggregateAnalysis(raw parser.Expr) bool {
	var children []parser.Expr
	switch e := raw.(type) {
	case parser.XMLConstructExpr:
		for _,child:=range e.Children(){if ContainsValuesAggregateAnalysis(child){return true}}
		return false
	case parser.XMLDocumentPredicateExpr:
		return ContainsValuesAggregateAnalysis(e.Child)
	case parser.XMLParseExpr:
		return ContainsValuesAggregateAnalysis(e.Child)
	case parser.XMLSerializeExpr:
		return ContainsValuesAggregateAnalysis(e.Child)
	case parser.AggregateExpr:
		if e.Name == "sum" || e.Name == "bit_and" || e.Name == "bit_or" || e.Name == "bit_xor" || functions.JSONArrayAggregateName(e.Name) || functions.JSONObjectAggregateName(e.Name) || functions.OrderedSetName(e.Name) {
			return true
		}
		for _, key := range e.Order {
			if ContainsValuesAggregateAnalysis(key.Expr) {
				return true
			}
		}
		if ContainsValuesAggregateAnalysis(e.Filter) {
			return true
		}
		children = e.Args
	case parser.CastExpr:
		return ContainsValuesAggregateAnalysis(e.Child)
	case parser.CollateExpr:
		return ContainsValuesAggregateAnalysis(e.Child)
	case parser.RegclassExpr:
		return ContainsValuesAggregateAnalysis(e.Child)
	case parser.TimeZoneExpr:
		return ContainsValuesAggregateAnalysis(e.Value) || !e.Local && ContainsValuesAggregateAnalysis(e.Zone)
	case parser.SQLJSONQueryExpr:
		children = e.Children()
	case parser.SQLJSONExpr:
		children = e.Args
	case parser.JSONPredicateExpr:
		children = []parser.Expr{e.Child}
	case parser.FunctionExpr:
		children = e.Args
	case parser.CallExpr:
		children = e.Args
	case parser.InListExpr:
		if ContainsValuesAggregateAnalysis(e.Left) {
			return true
		}
		children = e.Items
	case parser.ArrayComparisonExpr:
		return ContainsValuesAggregateAnalysis(e.Left) || ContainsValuesAggregateAnalysis(e.Right)
	case parser.ArrayExpr:
		children = e.Elements
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range e.Children(&storage) {
			if ContainsValuesAggregateAnalysis(child) {
				return true
			}
		}
		return false
	case parser.FieldExpr:
		return ContainsValuesAggregateAnalysis(e.Base)
	case parser.RowExpr:
		children = e.Fields
	case parser.SubqueryExpr:
		return ContainsValuesAggregateAnalysis(e.Left)
	case parser.OperatorExpr:
		children = e.Args
	case parser.ConditionalExpr:
		children = e.Args
	case parser.LogicalExpr:
		return ContainsValuesAggregateAnalysis(e.Left) || ContainsValuesAggregateAnalysis(e.Right)
	case parser.PredicateExpr:
		return ContainsValuesAggregateAnalysis(e.Left) || ContainsValuesAggregateAnalysis(e.Right)
	case parser.CaseExpr:
		if ContainsValuesAggregateAnalysis(e.Test) || ContainsValuesAggregateAnalysis(e.Else) {
			return true
		}
		for _, arm := range e.Arms {
			if ContainsValuesAggregateAnalysis(arm.Condition) || ContainsValuesAggregateAnalysis(arm.Result) {
				return true
			}
		}
	}
	for _, child := range children {
		if ContainsValuesAggregateAnalysis(child) {
			return true
		}
	}
	return false
}
