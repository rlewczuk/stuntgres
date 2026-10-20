package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Cache occurrence is independent of the retained physical frame index. Lock
// arbitration may reject rows or own replacement frames without changing rank.
func (s indexedStage) orderOccurrence(i int) int {
	if s.CacheOccurrences != nil {
		return s.CacheOccurrences[i]
	}
	if s.Permutation != nil {
		return s.Permutation[i]
	}
	return i
}
func (s indexedStage) OrderRow(i int) []sqltype.Value {
	if s.CacheWidth == 0 {
		return nil
	}
	occurrence := s.orderOccurrence(i)
	return s.OrderValues[occurrence*s.CacheWidth : (occurrence+1)*s.CacheWidth]
}

// Called only for a qualifying replacement frame. Original keys retain their
// cache slot identity; duplicate keys share the first evaluation just as in Sort.
// Frozen rank is never recomputed. Stable row-independent values remain reusable.
func RefreshLockedOrder(n *plan.SortNode, row []sqltype.Value, evaluation *EvalContext) error {
	next := 0
	evaluation.Row = row
	for _, key := range n.Keys {
		if key.Cache != next {
			continue
		}
		next++
		if !key.Output && !key.PeerDemand {
			continue
		}
		properties := key.Expression.Properties()
		if !properties.RowDependent && !properties.StageDependent && properties.Volatility != expr.Volatile {
			continue
		}
		if key.Cache >= len(evaluation.OrderValues) {
			return fmt.Errorf("stuntgres: missing replacement ordering cache")
		}
		value, err := key.Expression.Eval(evaluation)
		if err != nil {
			return err
		}
		if value.Type() != key.Expression.ResultUsage().Type {
			return fmt.Errorf("stuntgres: invalid replacement ordering value")
		}
		evaluation.OrderValues[key.Cache] = value
	}
	for i, bound := range n.Outputs {
		properties := bound.Properties()
		if !properties.RowDependent && !properties.StageDependent && properties.Volatility != expr.Volatile {
			continue
		}
		slot := n.CacheWidth - len(n.Outputs) + i
		if slot >= len(evaluation.OrderValues) {
			return fmt.Errorf("stuntgres: missing replacement output cache")
		}
		value, err := bound.Eval(evaluation)
		if err != nil {
			return err
		}
		if err := ValidateProducedValue(value, bound.ResultUsage()); err != nil {
			return err
		}
		evaluation.OrderValues[slot] = value
	}
	return nil
}

// Joined materialized stages carry occurrence IDs, not a StatementScope. Use
// the enclosing execution's manifest through every physical/view recursion.
func changedQueryLockSource(source *querySource, locks *queryLockContext) bool {
	if source.Input != nil {
		return changedQueryLockSource(source.Input, locks)
	}
	if source.Lineage != nil {
		return changedQueryLockSource(source.Lineage.Stage, locks)
	}
	if source.Singleton {
		return false
	}
	if source.Materialized {
		for _, occurrence := range source.Occurrences {
			if locks.Leaves[occurrence].Changed {
				return true
			}
		}
		return false
	}
	return locks.Leaves[source.Occurrence].Changed
}
