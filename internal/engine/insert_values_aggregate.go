package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// Inspect selected SUM/bitwise/JSON scalar composition without crossing a child query's frame.
// Only failed analysis (nil bound source) uses this check; assignment failures
// with an actual source retain the destination's ordinary diagnostic.
func containsValuesAggregateAnalysis(raw parser.Expr) bool {
	var children []parser.Expr
	switch e := raw.(type) {
	case parser.AggregateExpr:
		if e.Name == "sum" || e.Name == "bit_and" || e.Name == "bit_or" || e.Name == "bit_xor" || functions.JSONArrayAggregateName(e.Name) || functions.JSONObjectAggregateName(e.Name) || functions.OrderedSetName(e.Name) {
			return true
		}
		for _, key := range e.Order {
			if containsValuesAggregateAnalysis(key.Expr) {
				return true
			}
		}
		if containsValuesAggregateAnalysis(e.Filter) {
			return true
		}
		children = e.Args
	case parser.CastExpr:
		return containsValuesAggregateAnalysis(e.Child)
	case parser.CollateExpr:
		return containsValuesAggregateAnalysis(e.Child)
	case parser.RegclassExpr:
		return containsValuesAggregateAnalysis(e.Child)
	case parser.TimeZoneExpr:
		return containsValuesAggregateAnalysis(e.Value) || !e.Local && containsValuesAggregateAnalysis(e.Zone)
	case parser.SQLJSONExpr:
		children = e.Args
	case parser.JSONPredicateExpr:
		children = []parser.Expr{e.Child}
	case parser.FunctionExpr:
		children = e.Args
	case parser.CallExpr:
		children = e.Args
	case parser.InListExpr:
		if containsValuesAggregateAnalysis(e.Left) {
			return true
		}
		children = e.Items
	case parser.ArrayComparisonExpr:
		return containsValuesAggregateAnalysis(e.Left) || containsValuesAggregateAnalysis(e.Right)
	case parser.ArrayExpr:
		children = e.Elements
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range e.Children(&storage) {
			if containsValuesAggregateAnalysis(child) {
				return true
			}
		}
		return false
	case parser.FieldExpr:
		return containsValuesAggregateAnalysis(e.Base)
	case parser.RowExpr:
		children = e.Fields
	case parser.SubqueryExpr:
		return containsValuesAggregateAnalysis(e.Left)
	case parser.OperatorExpr:
		children = e.Args
	case parser.ConditionalExpr:
		children = e.Args
	case parser.LogicalExpr:
		return containsValuesAggregateAnalysis(e.Left) || containsValuesAggregateAnalysis(e.Right)
	case parser.PredicateExpr:
		return containsValuesAggregateAnalysis(e.Left) || containsValuesAggregateAnalysis(e.Right)
	case parser.CaseExpr:
		if containsValuesAggregateAnalysis(e.Test) || containsValuesAggregateAnalysis(e.Else) {
			return true
		}
		for _, arm := range e.Arms {
			if containsValuesAggregateAnalysis(arm.Condition) || containsValuesAggregateAnalysis(arm.Result) {
				return true
			}
		}
	}
	for _, child := range children {
		if containsValuesAggregateAnalysis(child) {
			return true
		}
	}
	return false
}
