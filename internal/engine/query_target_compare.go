package engine

import (
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// This comparison is binding-local: both operands have been fully analyzed in
// the same enclosing namespace. Child query allocation IDs are paired, while
// outer correlations retain their exact coordinates. It is not global CSE.
type queryTargetComparison struct {
	frames                [][2]uint32
	occurrences           []queryTargetOccurrence
	leftFrame, rightFrame uint32
}
type queryTargetOccurrence struct {
	leftFrame, rightFrame uint32
	left, right           int
}

// Query target resolution can normalize independently analyzed child frames;
// ordinary grouping comparison keeps its conservative identity contract.
func (b *expressionBinder) sameQueryTarget(a, c expr.Expr) bool {
	return b.sameGroupExpression(a, c) || b.sameSubqueryTarget(a, c)
}

func (b *expressionBinder) sameSubqueryTarget(a, c expr.Expr) bool {
	found := false
	walkBoundExpression(a, func(e expr.Expr) {
		if _, ok := e.(subqueryExpression); ok {
			found = true
		}
	})
	if !found {
		return false
	}
	var frames [16][2]uint32
	var occurrences [16]queryTargetOccurrence
	comparison := queryTargetComparison{frames: frames[:0], occurrences: occurrences[:0]}
	return b.sameGroupExpressionIn(a, c, &comparison)
}
func (c *queryTargetComparison) sameFrame(a, b uint32) bool {
	if c != nil {
		for _, p := range c.frames {
			if p[0] == a || p[1] == b {
				return p[0] == a && p[1] == b
			}
		}
	}
	return a == b
}
func (c *queryTargetComparison) pairFrame(a, b uint32) bool {
	for _, p := range c.frames {
		if p[0] == a || p[1] == b {
			return p[0] == a && p[1] == b
		}
	}
	c.frames = append(c.frames, [2]uint32{a, b})
	return true
}
func (c *queryTargetComparison) pairOccurrence(a, b int) bool {
	for _, p := range c.occurrences {
		if p.leftFrame == c.leftFrame && p.rightFrame == c.rightFrame && (p.left == a || p.right == b) {
			return p.left == a && p.right == b
		}
	}
	c.occurrences = append(c.occurrences, queryTargetOccurrence{c.leftFrame, c.rightFrame, a, b})
	return true
}
func (c *queryTargetComparison) expressions(b *expressionBinder, a, d []expr.Expr) bool {
	if len(a) != len(d) {
		return false
	}
	for i, e := range a {
		if !b.sameGroupExpressionIn(e, d[i], c) {
			return false
		}
	}
	return true
}
func (c *queryTargetComparison) order(b *expressionBinder, a, d []sortKey) bool {
	if len(a) != len(d) {
		return false
	}
	for i, k := range a {
		o := d[i]
		if k.comparison != o.comparison || k.outputOrdinal != o.outputOrdinal || k.targetIdentity != o.targetIdentity || k.slot != o.slot || k.cache != o.cache || k.desc != o.desc || k.nullsFirst != o.nullsFirst || k.output != o.output || k.peerDemand != o.peerDemand || !b.sameGroupExpressionIn(k.expression, o.expression, c) {
			return false
		}
	}
	return true
}
func (c *queryTargetComparison) window(b *expressionBinder, a, d boundWindowSpec) bool {
	return a.definition == d.definition && a.explicit == d.explicit && a.frame.unit == d.frame.unit && a.frame.exclusion == d.frame.exclusion && a.frame.start.kind == d.frame.start.kind && a.frame.end.kind == d.frame.end.kind && b.sameGroupExpressionIn(a.frame.start.offset, d.frame.start.offset, c) && b.sameGroupExpressionIn(a.frame.end.offset, d.frame.end.offset, c) && len(a.grouping) == len(d.grouping) && c.expressions(b, a.partition, d.partition) && c.order(b, a.order, d.order)
}

// Match logical policies and typed semantic expressions, not presentation ASTs,
// source character offsets, or arbitrary frame/source allocation numbers.
// Grouping/aggregate providers are deterministically bound from the compared
// typed arguments and operations in this same catalog/type universe.
func (c *queryTargetComparison) sameQuery(b *expressionBinder, a, d LogicalNode) bool {
	if a == nil || d == nil {
		return a == nil && d == nil
	}
	if len(a.Schema()) != len(d.Schema()) {
		return false
	}
	for i, col := range a.Schema() {
		if col != d.Schema()[i] {
			return false
		}
	}
	if n, ok := a.(*projectNode); ok {
		o, ok := d.(*projectNode)
		if !ok || !c.pairFrame(n.frame, o.frame) {
			return false
		}
		if n.syntax == nil || o.syntax == nil {
			if n.syntax != o.syntax {
				return false
			}
		} else if !sameQuerySourceAliases(n.syntax.From, o.syntax.From) {
			return false
		}
		left, right := c.leftFrame, c.rightFrame
		c.leftFrame, c.rightFrame = n.frame, o.frame
		defer func() { c.leftFrame, c.rightFrame = left, right }()
	}
	// Inputs register every local arm/derived frame before expression comparison.
	left, right := queryInputs(a)
	otherLeft, otherRight := queryInputs(d)
	if !c.sameQuery(b, left, otherLeft) || !c.sameQuery(b, right, otherRight) {
		return false
	}
	same := func(a, d expr.Expr) bool { return b.sameGroupExpressionIn(a, d, c) }
	switch n := a.(type) {
	case *functionFromNode:
		o, ok := d.(*functionFromNode)
		if !ok || n.ordinality != o.ordinality || n.correlated != o.correlated || len(n.calls) != len(o.calls) {
			return false
		}
		for i, call := range n.calls {
			if !same(call, o.calls[i]) {
				return false
			}
		}
		return true
	case *setProjectNode:
		o, ok := d.(*setProjectNode)
		if !ok || n.baseWidth != o.baseWidth || n.markerWidth != o.markerWidth ||
			len(n.calls) != len(o.calls) || !slices.Equal(n.callSlots, o.callSlots) || !slices.Equal(n.scalarSlots, o.scalarSlots) || !slices.Equal(n.steps, o.steps) || !c.expressions(b, n.scalars, o.scalars) {
			return false
		}
		if (n.template == nil) != (o.template == nil) {
			return false
		}
		if n.template != nil && (n.template.attempts != o.template.attempts || !slices.Equal(n.template.steps, o.template.steps) || !slices.Equal(n.template.scalarRequests, o.template.scalarRequests) || !c.expressions(b, n.template.expressions, o.template.expressions) ||
			!c.expressions(b, n.template.origins, o.template.origins) || !c.expressions(b, n.template.scalars, o.template.scalars)) {
			return false
		}
		for i, call := range n.calls {
			if !same(call, o.calls[i]) {
				return false
			}
		}
		return true
	case *projectNode:
		o, ok := d.(*projectNode)
		if !ok || n.frozenValues != o.frozenValues || n.hasTargetSet != o.hasTargetSet {
			return false
		}
		return c.expressions(b, n.expressions, o.expressions) && c.expressions(b, n.analysisExpressions, o.analysisExpressions)
	case *sourceNode:
		o, ok := d.(*sourceNode)
		return ok && n.ref == o.ref && c.pairOccurrence(n.occurrence, o.occurrence)
	case *singletonNode:
		_, ok := d.(*singletonNode)
		return ok
	case *valuesNode:
		o, ok := d.(*valuesNode)
		if !ok || len(n.rows) != len(o.rows) || !slices.Equal(n.identityOrderDemand, o.identityOrderDemand) {
			return false
		}
		for i, row := range n.rows {
			if !c.expressions(b, row, o.rows[i]) {
				return false
			}
		}
		return true
	case *filterNode:
		o, ok := d.(*filterNode)
		if !ok || !same(n.predicate, o.predicate) {
			return false
		}
	case *sortNode:
		o, ok := d.(*sortNode)
		if !ok || n.cacheWidth != o.cacheWidth || n.presenceOnly != o.presenceOnly || !c.order(b, n.keys, o.keys) || !c.expressions(b, n.outputs, o.outputs) {
			return false
		}
	case *evaluationNode:
		o, ok := d.(*evaluationNode)
		if !ok || !c.expressions(b, n.expressions, o.expressions) {
			return false
		}
	case *distinctNode:
		o, ok := d.(*distinctNode)
		if !ok || n.presenceOnly != o.presenceOnly || len(n.grouping) != len(o.grouping) || !c.expressions(b, n.expressions, o.expressions) {
			return false
		}
	case *distinctOnNode:
		o, ok := d.(*distinctOnNode)
		if !ok || n.presenceOnly != o.presenceOnly || len(n.grouping) != len(o.grouping) || !c.expressions(b, n.expressions, o.expressions) {
			return false
		}
	case *limitNode:
		o, ok := d.(*limitNode)
		if !ok || n.withTies != o.withTies || n.presenceOnly != o.presenceOnly || !same(n.offset, o.offset) || !same(n.count, o.count) || !c.order(b, n.peerKeys, o.peerKeys) {
			return false
		}
	case *joinNode:
		o, ok := d.(*joinNode)
		if !ok || n.kind != o.kind || n.leftWidth != o.leftWidth || n.rightWidth != o.rightWidth || n.correlatedRight != o.correlatedRight || n.correlationPrefix != o.correlationPrefix || !same(n.predicate, o.predicate) || !c.expressions(b, n.merges, o.merges) {
			return false
		}
	case *derivedNode:
		o, ok := d.(*derivedNode)
		if !ok || n.cte != o.cte || !slices.Equal(n.demand, o.demand) || (n.query == nil) != (o.query == nil) {
			return false
		}
		if n.query != nil && (n.query.regclassOutput != o.query.regclassOutput || n.query.visibilitySnapshot != o.query.visibilitySnapshot) {
			return false
		}
	case *setNode:
		o, ok := d.(*setNode)
		if !ok || n.operation != o.operation || n.all != o.all || len(n.grouping) != len(o.grouping) || !c.expressions(b, n.leftExpressions, o.leftExpressions) || !c.expressions(b, n.rightExpressions, o.rightExpressions) {
			return false
		}
	case *rowLockNode:
		o, ok := d.(*rowLockNode)
		if !ok || n.firstMode != o.firstMode || !slices.Equal(n.marks, o.marks) {
			return false
		}
	case *aggregateNode:
		o, ok := d.(*aggregateNode)
		if !ok || n.groupingActive != o.groupingActive || n.presenceOnly != o.presenceOnly || len(n.grouping) != len(o.grouping) || len(n.sets) != len(o.sets) || len(n.calls) != len(o.calls) || !c.expressions(b, n.keys, o.keys) {
			return false
		}
		for i, set := range n.sets {
			if !slices.Equal(set, o.sets[i]) {
				return false
			}
		}
		for i, call := range n.calls {
			other := o.calls[i]
			if call.operation != other.operation || call.includeNulls != other.includeNulls || call.provider.ResultUsage() != other.provider.ResultUsage() || call.stageWidth != other.stageWidth || len(call.distinct) != len(other.distinct) || !same(call.argument, other.argument) || !same(call.separator, other.separator) || !c.expressions(b, call.direct, other.direct) || !same(call.filter, other.filter) || !c.order(b, call.order, other.order) {
				return false
			}
		}
	case *windowNode:
		o, ok := d.(*windowNode)
		if !ok || len(n.specs) != len(o.specs) || len(n.calls) != len(o.calls) || !slices.Equal(n.specIndices, o.specIndices) {
			return false
		}
		for i, spec := range n.specs {
			if !c.window(b, spec, o.specs[i]) {
				return false
			}
		}
		for i, call := range n.calls {
			other := o.calls[i]
			if call.operation != other.operation || !same(call.argument, other.argument) || !same(call.ordinal, other.ordinal) || !same(call.defaultValue, other.defaultValue) || !same(call.expression, other.expression) || !c.window(b, call.spec, other.spec) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

// SQL99 compares analyzed RTE alias namespaces as well as resolved expressions.
// Keep only that identity from the retained syntax: Var qualification, source
// positions and raw predicates are not semantic comparison authorities.
func sameQuerySourceAliases(a, b parser.FromItem) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch n := a.(type) {
	case *parser.FunctionTable:
		o, ok := b.(*parser.FunctionTable)
		return ok && n.RowsFrom == o.RowsFrom
	case *parser.AliasReference:
		o, ok := b.(*parser.AliasReference)
		if !ok || n.Name.Text != o.Name.Text || len(n.Columns) != len(o.Columns) {
			return false
		}
		for i, name := range n.Columns {
			if name.Text != o.Columns[i].Text {
				return false
			}
		}
		return sameQuerySourceAliases(n.Input, o.Input)
	case *parser.JoinReference:
		o, ok := b.(*parser.JoinReference)
		if !ok {
			return false
		}
		if n.UsingAlias == nil || o.UsingAlias == nil {
			if n.UsingAlias != o.UsingAlias {
				return false
			}
		} else if n.UsingAlias.Text != o.UsingAlias.Text {
			return false
		}
		return sameQuerySourceAliases(n.Left, o.Left) && sameQuerySourceAliases(n.Right, o.Right)
	case *parser.TableReference:
		_, ok := b.(*parser.TableReference)
		return ok
	case *parser.DerivedQuery:
		_, ok := b.(*parser.DerivedQuery)
		return ok
	case *parser.ValuesQuery:
		_, ok := b.(*parser.ValuesQuery)
		return ok
	case *parser.FunctionFrom:
		o, ok := b.(*parser.FunctionFrom)
		return ok && n.Ordinality == o.Ordinality && n.Lateral == o.Lateral && len(n.Calls) == len(o.Calls)
	default:
		return false
	}
}
