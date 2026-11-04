package catalogrt

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// FunctionStatsDemand belongs to one admitted execution occurrence. Getter
// initialization survives some empty-input proofs; provider capture does not.
type FunctionStatsDemand struct {
	Getters uint8
	ReadInput bool
}

func FunctionStatsOrdinary(uses []int) FunctionStatsDemand {
	d := FunctionStatsDemand{Getters: 1, ReadInput: true}
	if uses == nil { d.Getters = 7; return d }
	for _, column := range uses { if column >= 3 && column <= 5 { d.Getters |= 1 << (column-3) } }
	return d
}

// FunctionStats proves only owned direct-input elimination and an unused,
// uncorrelated nullable LEFT JOIN side. Unsupported shapes retain ordinary
// demand. No expression evaluation other than imported BOOL/literal proof is
// permitted here, and no retained plan or authority is modified.
func (q *KeepQuery) FunctionStats(ctx context.Context, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, leaves []exec.Leaf, i int, mark plan.RowMark, locks *exec.LockContext) (FunctionStatsDemand, error) {
	d := FunctionStatsOrdinary(leaves[i].Uses)
	if err := ctx.Err(); err != nil { return d, err }
	if q == nil || mark.Mode != 0 || locks != nil || !sameLeafTables(q.Leaves, leaves) || leaves[i].Relation.Address().ObjectID != pgcatalog.StatUserFunctionsOID { return d, nil }
	project, ok := q.Root.(*plan.ProjectNode)
	if !ok || project.Frame != 1 || project.CTEOwner || project.RuleInvocation { return d, nil }
	evaluation := exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: project.Frame}, EvalStatement: exec.EvalStatement{Context: ctx, Parameters: q.Parameters, Input: input, Output: output}}
	var rejected bool
	var target uint8
	collect := func(bound expr.Expr) {
		plan.WalkExpression(bound, func(bound expr.Expr) {
			if column, ok := bound.(plan.ColumnExpr); ok && functionStatsColumn(column, leaves[i], project.Frame) && column.Slot >= 3 && column.Slot <= 5 { target |= 1 << (column.Slot-3) }
		})
	}
	node := plan.Node(project)
	for {
		if err := ctx.Err(); err != nil { return d, err }
		switch n := node.(type) {
		case *plan.ProjectNode:
			if n != project { return d, nil }
			for _, e := range n.Expressions { collect(e) }; node = n.Child
		case *plan.SortNode:
			for _, key := range n.Keys { collect(key.Expression) }; node = n.Child
		case *plan.LimitNode:
			node = n.Child // LIMIT 0 never removes initialization demand.
		case *plan.AggregateNode:
			// Aggregate arguments are input expressions, discarded with input.
			node = n.Child
		case *plan.FilterNode:
			noRows, err := functionStatsFalse(&evaluation, n.Predicate)
			if err != nil { return d, err }; rejected = rejected || noRows; node = n.Child
		case *plan.SourceNode:
			if len(leaves) != 1 || n.Occurrence != i || n.Ref != leaves[i].Ref || n.Sample != nil { return d, nil }
			if rejected { return FunctionStatsDemand{Getters: target}, nil }; return d, nil
		case *plan.DerivedNode:
			if !rejected || len(leaves) != 1 || n.CTE != nil || n.Query == nil || n.ScanFrame != project.Frame || project.HasTargetSet { return d, nil }
			filter, direct := project.Child.(*plan.FilterNode)
			if !direct || filter.Child != n { return d, nil }
			inner, ok := n.Query.Root.(*plan.ProjectNode)
			if !ok || inner.Syntax != leaves[i].Query || inner.CTEOwner || inner.RuleInvocation || inner.HasTargetSet { return d, nil }
			source, ok := inner.Child.(*plan.SourceNode)
			if !ok || source.Occurrence != i || source.Ref != leaves[i].Ref || source.Sample != nil { return d, nil }
			for _, bound := range inner.Expressions {
				identity, ok := bound.(plan.ColumnExpr)
				if !ok || !functionStatsColumn(identity, leaves[i], inner.Frame) || identity.Slot > 2 { return d, nil }
			}
			for _, bound := range project.Expressions {
				column, ok := bound.(plan.ColumnExpr)
				if !ok || column.Origin != (ids.ObjectRef{}) || column.Scope != 1 || column.Frame != n.ScanFrame || column.Virtual != nil || column.MergeSource || column.QueryOccurrence != 0 || column.RowWidth != 0 || column.Slot < 0 || column.Slot >= len(inner.Expressions) || column.Slot >= len(n.Schema()) { return d, nil }
				identity, ok := inner.Expressions[column.Slot].(plan.ColumnExpr)
				if !ok || !functionStatsColumn(identity, leaves[i], inner.Frame) || identity.Slot < 0 || identity.Slot > 2 || column.Usage != n.Schema()[column.Slot].Usage || identity.Usage != column.Usage { return d, nil }
			}
			return FunctionStatsDemand{}, nil
		case *plan.JoinNode:
			right, ok := n.Right.(*plan.SourceNode)
			literal, physical := n.Predicate.(plan.LiteralExpr)
			if !ok || !physical || n.Kind != parser.JoinLeft || n.Frame != project.Frame || n.CorrelatedRight || len(n.Captures) != 0 || len(n.Merges) != 0 || right.Occurrence != i || right.Ref != leaves[i].Ref || right.Sample != nil || len(leaves[i].Uses) != 0 { return d, nil }
			if literal.Value.Type() != nil && literal.Value.Type().ID() == sqltype.Bool && literal.Usage.Type == literal.Value.Type() { truth, err := plan.TruthOf(literal.Value); if err != nil { return d, err }; if truth != plan.True { return FunctionStatsDemand{}, nil } }
			return d, nil
		default:
			return d, nil
		}
	}
}

func functionStatsFalse(c *exec.Context, bound expr.Expr) (bool, error) {
	if logical, ok := bound.(plan.LogicalExpr); ok && logical.Op == parser.LogicalAnd && !logical.QueryRight {
		left, err := functionStatsFalse(c, logical.Left); if err != nil { return false, err }
		right, err := functionStatsFalse(c, logical.Right); return left || right, err
	}
	value, known, err := plan.QueryPredicateProofValue(c, bound)
	if err != nil || !known { return false, err }
	if value.Type() == nil || value.Type().ID() != sqltype.Bool || bound.ResultUsage().Type != value.Type() { return false, nil }
	truth, err := plan.TruthOf(value)
	return truth != plan.True, err
}

func functionStatsColumn(column plan.ColumnExpr, leaf exec.Leaf, frame uint32) bool {
	origin := column.Origin
	attribute := origin.Address.SubID
	origin.Address.SubID = 0
	return origin == leaf.Ref && column.Scope == 1 && column.Frame == frame && column.QueryOccurrence == 0 && column.Virtual == nil && !column.MergeSource && column.RowWidth == 0 && column.Slot >= 0 && column.Slot < len(leaf.Relation.Attributes) && attribute == leaf.Relation.Attributes[column.Slot]
}
