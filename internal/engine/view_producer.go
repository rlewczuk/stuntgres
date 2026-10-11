package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

func queryTreeOccurrences(node LogicalNode, inputs *querySource, dst []int) []int {
	switch n := node.(type) {
	case *sourceNode:
		return appendQueryOccurrences(dst, querySourceAt(inputs, n.occurrence))
	case *singletonNode, *valuesNode:
		return dst
	case *joinNode:
		return queryTreeOccurrences(n.right, inputs, queryTreeOccurrences(n.left, inputs, dst))
	case *derivedNode:
		return queryTreeOccurrences(n.query.Root, inputs, dst)
	case *projectNode:
		return queryTreeOccurrences(n.child, inputs, dst)
	case *filterNode:
		return queryTreeOccurrences(n.child, inputs, dst)
	case *sortNode:
		return queryTreeOccurrences(n.child, inputs, dst)
	case *windowNode:
		return dst // Windows consume their input and cannot carry row marks.
	case *aggregateNode, *distinctNode, *distinctOnNode, *setNode:
		return dst
	case *rowLockNode:
		return queryTreeOccurrences(n.child, inputs, dst)
	case *evaluationNode:
		return queryTreeOccurrences(n.child, inputs, dst)
	case *limitNode:
		return queryTreeOccurrences(n.child, inputs, dst)
	}
	panic("invalid view producer tree")
}

func openSourceStage(ctx context.Context, state *queryState, source *querySource, schema []ColumnDescriptor) (indexedStage, error) {
	if source.input != nil {
		child, err := openSourceStage(ctx, state, source.input, schema)
		if err != nil {
			return indexedStage{}, err
		}
		out := producingStage(&child, schema)
		out.source = source
		out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
			i, present, err := child.nextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			out.selection = append(out.selection, child.index(i))
			return true, ctx.Err()
		}
		return out, nil
	}
	if source.nestedRoot == nil {
		return indexedStage{source: source, schema: schema}, nil
	}
	// Materialize the current definition's complete row before an enclosing
	// frozen-slot bridge selects the attributes retained by a dependent view.
	producedSchema := source.nestedRoot.Schema()
	out := indexedStage{source: source, schema: schema, selection: []int{}, pending: pendingSourceMarks(source, nil)}
	out.finish = func() error {
		if source.nestedStage != nil {
			return source.nestedStage.Close()
		}
		return nil
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		if source.nestedStage == nil {
			project, ok := source.nestedRoot.(*projectNode)
			if !ok {
				return false, fmt.Errorf("stuntgres: view lacks final projection")
			}
			innerState := &queryState{source: source.nestedInputs, locking: source.locks.rowMarked(), input: state.input, output: state.output, inherited: source.inherited}
			inner, err := openQueryNode(ctx, innerState, project)
			if err != nil {
				return false, err
			}
			source.nestedStage = &inner
			if source.lineage != nil {
				source.lineage.stage = inner.source
				source.lineage.state = innerState
			}
		}
		inner := source.nestedStage
		i, present, err := inner.nextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		inner.evaluation.setContext(ctx)
		inner.evaluation.Row = inner.source.row(inner.index(i))
		inner.evaluation.orderValues = inner.orderRow(i)
		start := len(source.flat)
		source.flat = append(source.flat, make([]sqltype.Value, len(producedSchema))...)
		row := source.flat[start:]
		for j, expr := range inner.expressions {
			value, err := expr.Eval(&inner.evaluation)
			if err != nil {
				return false, err
			}
			if err := validateProducedValue(value, producedSchema[j].Usage); err != nil {
				return false, err
			}
			row[j] = value
		}
		if source.lineage != nil {
			lineage := source.lineage
			lineage.stage = inner.source
			lineage.selection = append(lineage.selection, inner.index(i))
			lineage.cacheWidth, lineage.orderValues = inner.cacheWidth, inner.orderValues
			if inner.cacheWidth != 0 {
				lineage.cacheOccurrences = append(lineage.cacheOccurrences, inner.orderOccurrence(i))
			}
		}
		out.selection = append(out.selection, source.flatCount)
		source.flatCount++
		return true, ctx.Err()
	}
	return out, nil
}
