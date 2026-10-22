package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func OpenDerivedNode(ctx context.Context, state *State, n *plan.DerivedNode) (Stage, error) {
	if n.CTE != nil && !n.CTE.Folded() {
		return OpenCTEScan(ctx, state, n)
	}
	project, ok := n.Query.Root.(*plan.ProjectNode)
	if !ok {
		return Stage{}, fmt.Errorf("stuntgres: derived query lacks final projection")
	}
	innerState := &State{Source: state.Source, SourceMode: state.subquerySourceMode(), Parameters: state.Parameters, Input: state.Input, Output: state.Output, Locking: state.Locking, Outer: state.Outer, Invocation: state.Invocation, foreignOwner: state.foreignOwner, Ctes: state.Ctes, Retain: state.Retain, hashOwner: state.membershipOwner()}
	inner, err := OpenQueryProjectStage(ctx, innerState, project)
	if err != nil {
		return Stage{}, err
	}
	source := &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host, Locks: state.Source.Locks}
	EstablishSourcePresence(source, n.PresenceSlot)
	if state.Source.Locks != nil {
		source.Lineage = &QueryLockLineage{Root: project, Inputs: state.Source, Stage: inner.Source, state: innerState}
	}
	if state.derived == nil {
		state.derived = make(map[*plan.DerivedNode]*Source)
	}
	state.derived[n] = source
	out := Stage{Source: source, Columns: n.Schema(), Selection: []int{}}
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
		position := inner.Position
		changed, err := inner.Rewind(ctx)
		if err != nil {
			return false, err
		}
		if !changed {
			// Kept derived captures must resume at the uncaptured child occurrence,
			// not append the cached prefix again after a parent rewind.
			inner.Position = position
			return false, nil
		}
		source.Flat, source.FlatCount = source.Flat[:0], 0
		out.Selection, out.Done = out.Selection[:0], false
		if source.Lineage != nil {
			source.Lineage.Selection = source.Lineage.Selection[:0]
			source.Lineage.cacheOccurrences = source.Lineage.cacheOccurrences[:0]
		}
		return true, nil
	}
	out.Finish = func() error {
		err := inner.Close()
		delete(state.derived, n)
		return err
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		i, present, err := inner.NextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		inner.Evaluation.setContext(ctx)
		inner.Evaluation.BindQueryRow(inner.Source, inner.Index(i))
		inner.Evaluation.OrderValues = inner.OrderRow(i)
		start := len(source.Flat)
		source.Flat = append(source.Flat, make([]sqltype.Value, len(n.Schema()))...)
		row := source.Flat[start:]
		for j, expr := range inner.Expressions {
			if n.CTE != nil && !n.Demand[j] {
				row[j] = sqltype.ResultNull(n.Schema()[j].Usage)
				continue
			}
			value, err := expr.Eval(&inner.Evaluation)
			if err != nil {
				return false, err
			}
			if err := ValidateProducedValue(value, n.Schema()[j].Usage); err != nil {
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
		out.Selection = append(out.Selection, source.FlatCount)
		source.FlatCount++
		return true, ctx.Err()
	}
	return out, nil
}
