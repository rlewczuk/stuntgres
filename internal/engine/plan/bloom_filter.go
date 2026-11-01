package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type BloomProbe struct {
	Column ColumnExpr
	Value expr.Expr
}

// BloomFilterProbes extracts only proven ordinary equality conjuncts. It never
// descends OR/NOT or another query frame. All other predicates remain residual.
func BloomFilterProbes(n *FilterNode, ref ids.ObjectRef, scope, frame uint32) []BloomProbe {
	var probes []BloomProbe
	var visit func(expr.Expr)
	visit = func(e expr.Expr) {
		if logical, ok := e.(LogicalExpr); ok {
			if logical.Op == parser.LogicalAnd && !logical.QueryRight { visit(logical.Left); visit(logical.Right) }
			return
		}
		column, value, ok := HashFilterProbe(&FilterNode{Predicate:e},ref,scope,frame)
		if ok { probes=append(probes,BloomProbe{Column:column,Value:value}) }
	}
	visit(n.Predicate)
	return probes
}
