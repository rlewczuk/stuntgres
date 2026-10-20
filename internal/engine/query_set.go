package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func setMultiplicity(operation string, all bool, left, right int) int {
	switch operation {
	case "union":
		if all {
			return left + right
		}
		if left+right != 0 {
			return 1
		}
	case "intersect":
		if all {
			return min(left, right)
		}
		if left != 0 && right != 0 {
			return 1
		}
	case "except":
		if all {
			return max(left-right, 0)
		}
		if left != 0 && right == 0 {
			return 1
		}
	}
	return 0
}

type setArm struct {
	inner       indexedStage
	expressions []expr.Expr
	schema      []catalog.Column
	row         []sqltype.Value
}

func openSetArm(ctx context.Context, state *queryState, project *plan.ProjectNode, expressions []expr.Expr, schema []catalog.Column) (setArm, error) {
	return openSetArmScratch(ctx, state, project, expressions, schema, nil)
}

func openSetArmScratch(ctx context.Context, state *queryState, project *plan.ProjectNode, expressions []expr.Expr, schema []catalog.Column, scratch []sqltype.Value) (setArm, error) {
	innerState := &queryState{source: state.source, parameters: state.parameters, input: state.input, output: state.output, locking: state.locking, outer: state.outer, ctes: state.ctes, hashOwner: state.membershipOwner()}
	inner, err := openQueryProjectStage(ctx, innerState, project)
	if err != nil {
		return setArm{}, err
	}
	if scratch == nil {
		scratch = make([]sqltype.Value, len(schema))
	}
	return setArm{inner: inner, expressions: expressions, schema: schema, row: scratch}, nil
}

func (a *setArm) Close() error { return a.inner.Close() }

// The arm scratch is reused on every pull; only unique keys or demanded UNION
// ALL outputs are retained by the parent. Arm-local clauses execute first.
func (a *setArm) next(ctx context.Context) ([]sqltype.Value, bool, error) {
	i, present, err := a.inner.nextOccurrence(ctx)
	if err != nil || !present {
		return nil, present, err
	}
	a.inner.evaluation.setContext(ctx)
	a.inner.evaluation.bindQueryRow(a.inner.source, a.inner.index(i))
	a.inner.evaluation.orderValues = a.inner.orderRow(i)
	for j, expression := range a.expressions {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		value, err := expression.Eval(&a.inner.evaluation)
		if err != nil {
			return nil, false, err
		}
		if err := validateProducedValue(value, a.schema[j].Usage); err != nil {
			return nil, false, err
		}
		a.row[j] = value
	}
	return a.row, true, ctx.Err()
}

func openSetNode(ctx context.Context, state *queryState, n *plan.SetNode) (out indexedStage, err error) {
	if n.Operation == "union" && n.All {
		return openUnionAll(ctx, state, n)
	}
	if len(n.Grouping) != len(n.Schema()) {
		return out, fmt.Errorf("stuntgres: invalid set grouping schema")
	}
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}
	buckets := make(map[uint64][]int)
	var counts [][2]int
	for side, arm := range [2]*plan.ProjectNode{n.Left, n.Right} {
		expressions := n.LeftExpressions
		if side == 1 {
			expressions = n.RightExpressions
		}
		child, failure := openSetArm(ctx, state, arm, expressions, n.Schema())
		if failure != nil {
			return indexedStage{}, failure
		}
		failure = func() (failure error) {
			defer func() {
				if closeErr := child.Close(); failure == nil {
					failure = closeErr
				}
			}()
			for {
				row, present, err := child.next(ctx)
				if err != nil {
					return err
				}
				if !present {
					break
				}
				hash := uint64(14695981039346656037)
				for j, family := range n.Grouping {
					value, err := family.PrepareKey(ctx, row[j])
					if err != nil {
						return err
					}
					row[j] = value
					h, err := family.Hash(ctx, value)
					if err != nil {
						return err
					}
					hash = (hash ^ h) * 1099511628211
				}
				index, err := findDistinctTuple(ctx, n.Grouping, row, source.flat, buckets[hash])
				if err != nil {
					return err
				}
				if index < 0 {
					index = source.flatCount
					buckets[hash] = append(buckets[hash], index)
					source.flat = append(source.flat, row...)
					source.flatCount++
					counts = append(counts, [2]int{})
				}
				counts[index][side]++
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			return ctx.Err()
		}()
		if failure != nil {
			return indexedStage{}, failure
		}
	}
	out = indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	for i, count := range counts {
		for copies := setMultiplicity(n.Operation, n.All, count[0], count[1]); copies > 0; copies-- {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			out.selection = append(out.selection, i)
		}
	}
	return out, ctx.Err()
}

func openUnionAll(ctx context.Context, state *queryState, n *plan.SetNode) (indexedStage, error) {
	out := indexedStage{source: &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}, schema: n.Schema(), selection: []int{}}
	var child setArm
	opened, side := false, 0
	out.finish = func() error {
		if opened {
			opened = false
			return child.Close()
		}
		return nil
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		for side < 2 {
			if !opened {
				arm, expressions := n.Left, n.LeftExpressions
				if side == 1 {
					arm, expressions = n.Right, n.RightExpressions
				}
				var err error
				child, err = openSetArm(ctx, state, arm, expressions, n.Schema())
				if err != nil {
					return false, err
				}
				opened = true
			}
			row, present, err := child.next(ctx)
			if err != nil {
				return false, err
			}
			if !present {
				opened = false
				if err := child.Close(); err != nil {
					return false, err
				}
				side++
				continue
			}
			out.source.flat = append(out.source.flat, row...)
			out.selection = append(out.selection, out.source.flatCount)
			out.source.flatCount++
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	return out, nil
}

func preflightSet(c *EvalContext, n *plan.SetNode) (plan.Node, error) {
	left, err := preflightQuery(c, n.Left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.Right)
	if err != nil {
		return nil, err
	}
	copy := *n
	copy.Left, copy.Right = left.(*plan.ProjectNode), right.(*plan.ProjectNode)
	changed := left != n.Left || right != n.Right
	for side, expressions := range [2][]expr.Expr{n.LeftExpressions, n.RightExpressions} {
		var next []expr.Expr
		for i, expression := range expressions {
			folded, different, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if different {
				if next == nil {
					next = append([]expr.Expr(nil), expressions...)
				}
				next[i] = folded
				changed = true
			}
		}
		if next != nil {
			if side == 0 {
				copy.LeftExpressions = next
			} else {
				copy.RightExpressions = next
			}
		}
	}
	if changed {
		return &copy, nil
	}
	return n, nil
}
