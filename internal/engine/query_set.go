package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// setNode describes common-typed projected arms. No execution state or physical
// lineage is retained here. UNION ALL intentionally has no equivalence provider.
type setNode struct {
	querySchema
	left, right                       *projectNode
	operation                         string
	all                               bool
	leftExpressions, rightExpressions []BoundExpr
	grouping                          []sqltype.Grouping
}

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
	expressions []BoundExpr
	schema      []ColumnDescriptor
	row         []sqltype.Value
}

func openSetArm(ctx context.Context, state *queryState, project *projectNode, expressions []BoundExpr, schema []ColumnDescriptor) (setArm, error) {
	innerState := &queryState{source: state.source, parameters: state.parameters, input: state.input, output: state.output, locking: state.locking, outer: state.outer}
	inner, err := openQueryProjectStage(ctx, innerState, project)
	if err != nil {
		return setArm{}, err
	}
	return setArm{inner: inner, expressions: expressions, schema: schema, row: make([]sqltype.Value, len(schema))}, nil
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
	a.inner.evaluation.Row = a.inner.source.row(a.inner.index(i))
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

func openSetNode(ctx context.Context, state *queryState, n *setNode) (out indexedStage, err error) {
	if n.operation == "union" && n.all {
		return openUnionAll(ctx, state, n)
	}
	if len(n.grouping) != len(n.Schema()) {
		return out, fmt.Errorf("stuntgres: invalid set grouping schema")
	}
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}
	buckets := make(map[uint64][]int)
	var counts [][2]int
	for side, arm := range [2]*projectNode{n.left, n.right} {
		expressions := n.leftExpressions
		if side == 1 {
			expressions = n.rightExpressions
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
				for j, family := range n.grouping {
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
				index, err := findDistinctTuple(ctx, n.grouping, row, source.flat, buckets[hash])
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
		for copies := setMultiplicity(n.operation, n.all, count[0], count[1]); copies > 0; copies-- {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			out.selection = append(out.selection, i)
		}
	}
	return out, ctx.Err()
}

func openUnionAll(ctx context.Context, state *queryState, n *setNode) (indexedStage, error) {
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
				arm, expressions := n.left, n.leftExpressions
				if side == 1 {
					arm, expressions = n.right, n.rightExpressions
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

func preflightSet(c *EvalContext, n *setNode) (LogicalNode, error) {
	left, err := preflightQuery(c, n.left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.right)
	if err != nil {
		return nil, err
	}
	copy := *n
	copy.left, copy.right = left.(*projectNode), right.(*projectNode)
	changed := left != n.left || right != n.right
	for side, expressions := range [2][]BoundExpr{n.leftExpressions, n.rightExpressions} {
		var next []BoundExpr
		for i, expression := range expressions {
			folded, different, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if different {
				if next == nil {
					next = append([]BoundExpr(nil), expressions...)
				}
				next[i] = folded
				changed = true
			}
		}
		if next != nil {
			if side == 0 {
				copy.leftExpressions = next
			} else {
				copy.rightExpressions = next
			}
		}
	}
	if changed {
		return &copy, nil
	}
	return n, nil
}
