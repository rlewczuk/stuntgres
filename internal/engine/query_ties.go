package engine

import (
	"context"
	"stuntgres/internal/sqltype"
)

// Peer keys borrow the finished ORDER authority; no expressions are evaluated
// by LIMIT. Datum values own their payload even when a source borrows row headers.
func queryTiesValue(child *indexedStage, occurrence int, key sortKey) sqltype.Value {
	if key.cache >= 0 {
		return child.orderRow(occurrence)[key.cache]
	}
	return child.source.row(child.index(occurrence))[key.slot]
}

func captureQueryTiesBoundary(child *indexedStage, occurrence int, keys []sortKey, boundary []sqltype.Value) {
	for i, key := range keys {
		boundary[i] = queryTiesValue(child, occurrence, key)
	}
}

func queryTiesPeer(ctx context.Context, child *indexedStage, occurrence int, keys []sortKey, boundary []sqltype.Value, input sqltype.InputEnvironment) (bool, error) {
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		value, last := queryTiesValue(child, occurrence, key), boundary[i]
		if value.IsNull() && last.IsNull() {
			continue
		}
		if value.IsNull() || last.IsNull() {
			return false, nil
		}
		sign, err := key.comparison.Compare(ctx, value, last, input)
		if err != nil {
			return false, err
		}
		if sign != 0 {
			return false, nil
		}
	}
	return true, ctx.Err()
}
