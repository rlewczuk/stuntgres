package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Cache occurrence is independent of the retained physical frame index. Lock
// arbitration may reject rows or own replacement frames without changing rank.
func (s indexedStage) orderOccurrence(i int) int {
	if s.cacheOccurrences != nil {
		return s.cacheOccurrences[i]
	}
	if s.permutation != nil {
		return s.permutation[i]
	}
	return i
}
func (s indexedStage) orderRow(i int) []sqltype.Value {
	if s.cacheWidth == 0 {
		return nil
	}
	occurrence := s.orderOccurrence(i)
	return s.orderValues[occurrence*s.cacheWidth : (occurrence+1)*s.cacheWidth]
}

// Called only for a qualifying replacement frame. Original keys retain their
// cache slot identity; duplicate keys share the first evaluation just as in Sort.
// Frozen rank is never recomputed. Stable row-independent values remain reusable.
func refreshLockedOrder(n *sortNode, row []sqltype.Value, evaluation *EvalContext) error {
	next := 0
	evaluation.Row = row
	for _, key := range n.keys {
		if key.cache != next {
			continue
		}
		next++
		if !key.output && !key.peerDemand {
			continue
		}
		properties := key.expression.Properties()
		if !properties.RowDependent && !properties.StageDependent && properties.Volatility != expr.Volatile {
			continue
		}
		if key.cache >= len(evaluation.orderValues) {
			return fmt.Errorf("stuntgres: missing replacement ordering cache")
		}
		value, err := key.expression.Eval(evaluation)
		if err != nil {
			return err
		}
		if value.Type() != key.expression.ResultUsage().Type {
			return fmt.Errorf("stuntgres: invalid replacement ordering value")
		}
		evaluation.orderValues[key.cache] = value
	}
	for i, bound := range n.outputs {
		properties := bound.Properties()
		if !properties.RowDependent && !properties.StageDependent && properties.Volatility != expr.Volatile {
			continue
		}
		slot := n.cacheWidth - len(n.outputs) + i
		if slot >= len(evaluation.orderValues) {
			return fmt.Errorf("stuntgres: missing replacement output cache")
		}
		value, err := bound.Eval(evaluation)
		if err != nil {
			return err
		}
		if err := validateProducedValue(value, bound.ResultUsage()); err != nil {
			return err
		}
		evaluation.orderValues[slot] = value
	}
	return nil
}

// Joined materialized stages carry occurrence IDs, not a StatementScope. Use
// the enclosing execution's manifest through every physical/view recursion.
func changedQueryLockSource(source *querySource, locks *queryLockContext) bool {
	if source.input != nil {
		return changedQueryLockSource(source.input, locks)
	}
	if source.lineage != nil {
		return changedQueryLockSource(source.lineage.stage, locks)
	}
	if source.singleton {
		return false
	}
	if source.materialized {
		for _, occurrence := range source.occurrences {
			if locks.leaves[occurrence].changed {
				return true
			}
		}
		return false
	}
	return locks.leaves[source.occurrence].changed
}
