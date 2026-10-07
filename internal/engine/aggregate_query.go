package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type aggregateNode struct {
	querySchema
	child     LogicalNode
	keys      []BoundExpr
	grouping  []sqltype.Grouping
	providers []aggregateProvider
}
type queryGroup struct {
	keys   []sqltype.Value
	states []any
}

func openAggregateNode(ctx context.Context, state *queryState, n *aggregateNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	if state.source.locks != nil {
		original := child
		child, err = arbitrateViewQuery(ctx, state, n.child, original)
		original.Close()
		if err != nil {
			return indexedStage{}, err
		}
	}
	defer child.Close()
	groups := make([]queryGroup, 0)
	var buckets map[uint64][]int
	if len(n.keys) > 0 {
		buckets = make(map[uint64][]int)
	}
	scratch := make([]sqltype.Value, len(n.keys))
	newGroup := func() (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		group := queryGroup{keys: make([]sqltype.Value, len(n.Schema())), states: make([]any, len(n.providers))}
		copy(group.keys, scratch)
		for i, provider := range n.providers {
			var err error
			group.states[i], err = provider.Init(ctx)
			if err != nil {
				return 0, err
			}
			if err = ctx.Err(); err != nil {
				return 0, err
			}
		}
		groups = append(groups, group)
		return len(groups) - 1, nil
	}
	if len(n.keys) == 0 {
		if _, err := newGroup(); err != nil {
			return indexedStage{}, err
		}
	}
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: state.source.scope, Input: state.input, Output: state.output}
	for i := range child.count() {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		evaluation.Row = child.source.row(child.index(i))
		hash := uint64(14695981039346656037)
		for k, key := range n.keys {
			value, err := key.Eval(&evaluation)
			if err != nil {
				return indexedStage{}, err
			}
			if err = ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			scratch[k] = value
			h, err := n.grouping[k].Hash(ctx, value)
			if err != nil {
				return indexedStage{}, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index := 0
		if len(n.keys) > 0 {
			index, err = findQueryGroup(ctx, n.grouping, scratch, groups, buckets[hash])
			if err != nil {
				return indexedStage{}, err
			}
			if index < 0 {
				index, err = newGroup()
				if err != nil {
					return indexedStage{}, err
				}
				buckets[hash] = append(buckets[hash], index)
			}
		}
		for p, provider := range n.providers {
			if err := provider.Advance(ctx, groups[index].states[p]); err != nil {
				return indexedStage{}, err
			}
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
		}
	}
	rows := make([][]sqltype.Value, len(groups))
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		row := group.keys
		for p, provider := range n.providers {
			value, err := provider.Finalize(ctx, group.states[p])
			if err != nil {
				return indexedStage{}, err
			}
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			if value.Type() != provider.ResultUsage().Type {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid finalized aggregate value")
			}
			row[len(n.keys)+p] = value
		}
		rows[i] = row
	}
	// Transfer this execution owner's layout only after every group finalizes.
	// The original immutable relation pin is no longer needed by downstream work.
	source := child.source
	source.close()
	source.anonymous = rows
	return indexedStage{source: source, schema: n.Schema()}, nil
}

func findQueryGroup(ctx context.Context, grouping []sqltype.Grouping, keys []sqltype.Value, groups []queryGroup, candidates []int) (int, error) {
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		equal := true
		for k, value := range keys {
			same, err := grouping[k].Equal(ctx, value, groups[candidate].keys[k])
			if err != nil {
				return -1, err
			}
			if !same {
				equal = false
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		if equal {
			return candidate, nil
		}
	}
	return -1, ctx.Err()
}
