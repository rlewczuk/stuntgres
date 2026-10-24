package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func QueryTreeOccurrences(node plan.Node, inputs *Source, dst []int) []int {
	switch n := node.(type) {
	case *plan.SourceNode:
		return AppendQueryOccurrences(dst, QuerySourceAt(inputs, n.Occurrence))
	case *plan.SingletonNode, *plan.ValuesNode, *plan.FunctionFromNode, *plan.WorktableScanNode:
		return dst
	case *plan.JoinNode:
		return QueryTreeOccurrences(n.Right, inputs, QueryTreeOccurrences(n.Left, inputs, dst))
	case *plan.DerivedNode:
		if n.CTE != nil {
			return dst
		}
		return QueryTreeOccurrences(n.Query.Root, inputs, dst)
	case *plan.ProjectNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	case *plan.SetProjectNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	case *plan.FilterNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	case *plan.SortNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	case *plan.WindowNode:
		return dst // Windows consume their input and cannot carry row marks.
	case *plan.AggregateNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode:
		return dst
	case *plan.RowLockNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	case *plan.EvaluationNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	case *plan.LimitNode:
		return QueryTreeOccurrences(n.Child, inputs, dst)
	}
	panic("invalid view producer tree")
}

func OpenSourceStage(ctx context.Context, state *State, source *Source, schema []catalog.Column) (Stage, error) {
	if source.Input != nil {
		child, err := OpenSourceStage(ctx, state, source.Input, schema)
		if err != nil {
			return Stage{}, err
		}
		out := ProducingStage(&child, schema)
		out.Source = source
		out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
			i, present, err := child.NextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			out.Selection = append(out.Selection, child.Index(i))
			return true, ctx.Err()
		}
		return out, nil
	}
	if source.NestedRoot == nil {
		child := Stage{Source: source, Columns: schema}
		if source.RLS != nil {
			return openRowSecurityStage(ctx, state, child), nil
		}
		return child, nil
	}
	// Materialize the current definition's complete row before an enclosing
	// frozen-slot bridge selects the attributes retained by a dependent view.
	producedSchema := source.NestedRoot.Schema()
	// Explicit borrowed invocations retain the source-owned producer and read
	// its identity range. One-shot/rowstream owners keep required close vetoes.
	out := Stage{Source: source, Columns: schema, scanRange: true, scanEnd: source.FlatCount, Pending: pendingSourceMarks(source, nil)}
	if state.SourceMode != SourceBorrowed {
		out.Finish = func() error {
			if source.NestedStage != nil {
				return source.NestedStage.Close()
			}
			return nil
		}
	}
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
		if source.NestedStage == nil {
			return false, nil
		}
		if source.NestedStage.Terminal != nil {
			return false, source.NestedStage.Terminal
		}
		position := source.NestedStage.Position
		changed, err := source.NestedStage.Rewind(ctx)
		if err != nil {
			return false, err
		}
		if !changed {
			// Retained view rows still own the child's next-uncaptured frontier.
			source.NestedStage.Position = position
			return false, nil
		}
		source.Flat, source.FlatCount = source.Flat[:0], 0
		out.scanEnd, out.Done = 0, false
		if source.Lineage != nil {
			source.Lineage.Selection = source.Lineage.Selection[:0]
			source.Lineage.cacheOccurrences = source.Lineage.cacheOccurrences[:0]
		}
		return true, nil
	}
	out.onFailure = func(err error) error {
		if source.NestedStage != nil {
			return source.NestedStage.fail(err)
		}
		return err
	}
	// A borrowed short-circuiting reader replays stable captured rows, resuming
	// only the next-uncaptured frontier until its actual owner completes.
	if source.NestedStage != nil {
		if source.NestedStage.Terminal != nil {
			return Stage{}, source.NestedStage.Terminal
		}
		if _, err := out.Rewind(ctx); err != nil {
			return Stage{}, err
		}
		out.scanEnd = source.FlatCount
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		if source.NestedStage == nil {
			project, ok := source.NestedRoot.(*plan.ProjectNode)
			if !ok {
				return false, fmt.Errorf("stuntgres: view lacks final projection")
			}
			innerState := &State{Source: source.NestedInputs, SourceMode: state.SourceMode, Locking: source.Locks.RowMarked(), Input: state.Input, Output: state.Output, inherited: source.Inherited, Retain: state.Retain || state.SourceMode == SourceBorrowed, hashOwner: state.membershipOwner()}
			innerState.inheritAsyncAdmission(state)
			inner, err := OpenNode(ctx, innerState, project)
			if err != nil {
				return false, err
			}
			for _, expression := range inner.Expressions {
				if expression.Properties().Volatility != expr.Volatile {
					continue
				}
				reset := inner.Reset
				inner.Reset = func(ctx context.Context, stage *Stage) (bool, error) {
					if reset != nil {
						if _, err := reset(ctx, stage); err != nil {
							return false, err
						}
					}
					// Reuse captured physical occurrences, but reevaluate demanded
					// volatile outputs for the next invocation.
					return true, ctx.Err()
				}
				break
			}
			source.NestedStage = &inner
			if source.Lineage != nil {
				source.Lineage.Stage = inner.Source
				source.Lineage.state = innerState
			}
		}
		inner := source.NestedStage
		i, present, err := inner.NextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		inner.Evaluation.setContext(ctx)
		inner.Evaluation.BindQueryRow(inner.Source, inner.Index(i))
		inner.Evaluation.OrderValues = inner.OrderRow(i)
		start := len(source.Flat)
		source.Flat = append(source.Flat, make([]sqltype.Value, len(producedSchema))...)
		row := source.Flat[start:]
		for j, expr := range inner.Expressions {
			value, err := expr.Eval(&inner.Evaluation)
			if err != nil {
				return false, err
			}
			if err := ValidateProducedValue(value, producedSchema[j].Usage); err != nil {
				return false, err
			}
			row[j] = value
		}
		if source.Lineage != nil {
			lineage := source.Lineage
			lineage.Stage = inner.Source
			lineage.Selection = append(lineage.Selection, inner.Index(i))
			lineage.CacheWidth, lineage.orderValues = inner.CacheWidth, inner.OrderValues
			if inner.CacheWidth != 0 {
				lineage.cacheOccurrences = append(lineage.cacheOccurrences, inner.orderOccurrence(i))
			}
		}
		out.scanEnd = source.FlatCount + 1
		source.FlatCount++
		return true, ctx.Err()
	}
	return out, nil
}
