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
		if k.Comparison != o.Comparison || k.OutputOrdinal != o.OutputOrdinal || k.TargetIdentity != o.TargetIdentity || k.Slot != o.Slot || k.Cache != o.Cache || k.Desc != o.Desc || k.NullsFirst != o.NullsFirst || k.Output != o.Output || k.PeerDemand != o.PeerDemand || !b.sameGroupExpressionIn(k.Expression, o.Expression, c) {
			return false
		}
	}
	return true
}
func (c *queryTargetComparison) window(b *expressionBinder, a, d boundWindowSpec) bool {
	return a.Definition == d.Definition && a.Explicit == d.Explicit && a.Frame.Unit == d.Frame.Unit && a.Frame.Exclusion == d.Frame.Exclusion && a.Frame.Start.Kind == d.Frame.Start.Kind && a.Frame.End.Kind == d.Frame.End.Kind && b.sameGroupExpressionIn(a.Frame.Start.Offset, d.Frame.Start.Offset, c) && b.sameGroupExpressionIn(a.Frame.End.Offset, d.Frame.End.Offset, c) && len(a.Grouping) == len(d.Grouping) && c.expressions(b, a.Partition, d.Partition) && c.order(b, a.Order, d.Order)
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
		if !ok || !c.pairFrame(n.Frame, o.Frame) {
			return false
		}
		if n.Syntax == nil || o.Syntax == nil {
			if n.Syntax != o.Syntax {
				return false
			}
		} else if !sameQuerySourceAliases(n.Syntax.From, o.Syntax.From) {
			return false
		}
		left, right := c.leftFrame, c.rightFrame
		c.leftFrame, c.rightFrame = n.Frame, o.Frame
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
		if !ok || n.Ordinality != o.Ordinality || n.Correlated != o.Correlated || len(n.Calls) != len(o.Calls) {
			return false
		}
		for i, call := range n.Calls {
			if !same(call, o.Calls[i]) {
				return false
			}
		}
		return true
	case *setProjectNode:
		o, ok := d.(*setProjectNode)
		if !ok || n.BaseWidth != o.BaseWidth || n.MarkerWidth != o.MarkerWidth ||
			len(n.Calls) != len(o.Calls) || !slices.Equal(n.CallSlots, o.CallSlots) || !slices.Equal(n.ScalarSlots, o.ScalarSlots) || !slices.Equal(n.Steps, o.Steps) || !c.expressions(b, n.Scalars, o.Scalars) {
			return false
		}
		if (n.Template == nil) != (o.Template == nil) {
			return false
		}
		if n.Template != nil && (n.Template.Attempts != o.Template.Attempts || !slices.Equal(n.Template.Steps, o.Template.Steps) || !slices.Equal(n.Template.ScalarRequests, o.Template.ScalarRequests) || !c.expressions(b, n.Template.Expressions, o.Template.Expressions) ||
			!c.expressions(b, n.Template.Origins, o.Template.Origins) || !c.expressions(b, n.Template.Scalars, o.Template.Scalars)) {
			return false
		}
		for i, call := range n.Calls {
			if !same(call, o.Calls[i]) {
				return false
			}
		}
		return true
	case *projectNode:
		o, ok := d.(*projectNode)
		if !ok || n.FrozenValues != o.FrozenValues || n.HasTargetSet != o.HasTargetSet {
			return false
		}
		return c.expressions(b, n.Expressions, o.Expressions) && c.expressions(b, n.AnalysisExpressions, o.AnalysisExpressions)
	case *sourceNode:
		o, ok := d.(*sourceNode)
		return ok && n.Ref == o.Ref && c.pairOccurrence(n.Occurrence, o.Occurrence)
	case *singletonNode:
		_, ok := d.(*singletonNode)
		return ok
	case *valuesNode:
		o, ok := d.(*valuesNode)
		if !ok || len(n.Rows) != len(o.Rows) || !slices.Equal(n.IdentityOrderDemand, o.IdentityOrderDemand) {
			return false
		}
		for i, row := range n.Rows {
			if !c.expressions(b, row, o.Rows[i]) {
				return false
			}
		}
		return true
	case *filterNode:
		o, ok := d.(*filterNode)
		if !ok || !same(n.Predicate, o.Predicate) {
			return false
		}
	case *sortNode:
		o, ok := d.(*sortNode)
		if !ok || n.CacheWidth != o.CacheWidth || n.PresenceOnly != o.PresenceOnly || !c.order(b, n.Keys, o.Keys) || !c.expressions(b, n.Outputs, o.Outputs) {
			return false
		}
	case *evaluationNode:
		o, ok := d.(*evaluationNode)
		if !ok || !c.expressions(b, n.Expressions, o.Expressions) {
			return false
		}
	case *distinctNode:
		o, ok := d.(*distinctNode)
		if !ok || n.PresenceOnly != o.PresenceOnly || len(n.Grouping) != len(o.Grouping) || !c.expressions(b, n.Expressions, o.Expressions) {
			return false
		}
	case *distinctOnNode:
		o, ok := d.(*distinctOnNode)
		if !ok || n.PresenceOnly != o.PresenceOnly || len(n.Grouping) != len(o.Grouping) || !c.expressions(b, n.Expressions, o.Expressions) {
			return false
		}
	case *limitNode:
		o, ok := d.(*limitNode)
		if !ok || n.WithTies != o.WithTies || n.PresenceOnly != o.PresenceOnly || !same(n.Offset, o.Offset) || !same(n.Count, o.Count) || !c.order(b, n.PeerKeys, o.PeerKeys) {
			return false
		}
	case *joinNode:
		o, ok := d.(*joinNode)
		if !ok || n.Kind != o.Kind || n.LeftWidth != o.LeftWidth || n.RightWidth != o.RightWidth || n.CorrelatedRight != o.CorrelatedRight || n.CorrelationPrefix != o.CorrelationPrefix || !same(n.Predicate, o.Predicate) || !c.expressions(b, n.Merges, o.Merges) {
			return false
		}
	case *derivedNode:
		o, ok := d.(*derivedNode)
		if !ok || n.CTE != o.CTE || !slices.Equal(n.Demand, o.Demand) || (n.Query == nil) != (o.Query == nil) {
			return false
		}
		if n.Query != nil && (n.Query.RegclassOutput != o.Query.RegclassOutput || n.Query.VisibilitySnapshot != o.Query.VisibilitySnapshot) {
			return false
		}
	case *setNode:
		o, ok := d.(*setNode)
		if !ok || n.Operation != o.Operation || n.All != o.All || len(n.Grouping) != len(o.Grouping) || !c.expressions(b, n.LeftExpressions, o.LeftExpressions) || !c.expressions(b, n.RightExpressions, o.RightExpressions) {
			return false
		}
	case *rowLockNode:
		o, ok := d.(*rowLockNode)
		if !ok || n.FirstMode != o.FirstMode || !slices.Equal(n.Marks, o.Marks) {
			return false
		}
	case *aggregateNode:
		o, ok := d.(*aggregateNode)
		if !ok || n.GroupingActive != o.GroupingActive || n.PresenceOnly != o.PresenceOnly || len(n.Grouping) != len(o.Grouping) || len(n.Sets) != len(o.Sets) || len(n.Calls) != len(o.Calls) || !c.expressions(b, n.Keys, o.Keys) {
			return false
		}
		for i, set := range n.Sets {
			if !slices.Equal(set, o.Sets[i]) {
				return false
			}
		}
		for i, call := range n.Calls {
			other := o.Calls[i]
			if call.Operation != other.Operation || call.IncludeNulls != other.IncludeNulls || call.Provider.ResultUsage() != other.Provider.ResultUsage() || call.StageWidth != other.StageWidth || len(call.Distinct) != len(other.Distinct) || !same(call.Argument, other.Argument) || !same(call.Separator, other.Separator) || !c.expressions(b, call.Direct, other.Direct) || !same(call.Filter, other.Filter) || !c.order(b, call.Order, other.Order) {
				return false
			}
		}
	case *windowNode:
		o, ok := d.(*windowNode)
		if !ok || len(n.Specs) != len(o.Specs) || len(n.Calls) != len(o.Calls) || !slices.Equal(n.SpecIndices, o.SpecIndices) {
			return false
		}
		for i, spec := range n.Specs {
			if !c.window(b, spec, o.Specs[i]) {
				return false
			}
		}
		for i, call := range n.Calls {
			other := o.Calls[i]
			if call.Operation != other.Operation || !same(call.Argument, other.Argument) || !same(call.Ordinal, other.Ordinal) || !same(call.DefaultValue, other.DefaultValue) || !same(call.Expression, other.Expression) || !c.window(b, call.Spec, other.Spec) {
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
