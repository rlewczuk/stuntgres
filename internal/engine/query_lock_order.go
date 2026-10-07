package engine

import (
	"fmt"
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
		if !key.output {
			continue
		}
		properties := key.expression.Properties()
		if !properties.RowDependent && !properties.StageDependent && properties.Volatility != Volatile {
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
	return nil
}

func changedQueryLockSource(source *querySource) bool {
	if source.input != nil {
		return changedQueryLockSource(source.input)
	}
	if source.lineage != nil {
		return changedQueryLockSource(source.lineage.stage)
	}
	if source.singleton {
		return false
	}
	if source.materialized {
		for _, occurrence := range source.occurrences {
			if source.scope.queryLocks.leaves[occurrence].changed {
				return true
			}
		}
		return false
	}
	return source.scope.queryLocks.leaves[source.occurrence].changed
}
