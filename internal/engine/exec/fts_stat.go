package exec

import (
	"context"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
)

// ConsumeFTSStatistics executes the first bounded fetch even for invalid shapes.
// A domain retains the same immutable binary vector payload as its base type.
func ConsumeFTSStatistics(ctx context.Context, rows RowCursor, weights string, result *sqltype.Descriptor) (values []sqltype.Value, err error) {
	defer func() {
		if failure := rows.Close(); err == nil {
			err = failure
		}
		if err != nil {
			values = nil
		}
	}()
	schema := rows.Schema()
	if len(schema) != 1 || schema[0].Usage.Type == nil || schema[0].Usage.Type.BaseType().ID() != sqltype.TSVector {
		if _, err := fetchTextSearchBatch(ctx, rows, make([]sqltype.Value, len(schema)), len(schema), false); err != nil {
			return nil, err
		}
		return nil, sqlError("22023", "ts_stat query must return one tsvector column")
	}
	var batch [textSearchFetchSize]sqltype.Value
	counts := make(fts.Statistics)
	mask := fts.StatisticsWeights(weights)
	for {
		n, err := fetchTextSearchBatch(ctx, rows, batch[:], 1, true)
		if err != nil {
			return nil, err
		}
		for _, value := range batch[:n] {
			if err := value.AccumulateFTSStatistics(ctx, mask, counts); err != nil {
				return nil, err
			}
		}
		if n < textSearchFetchSize {
			break
		}
	}
	return sqltype.FTSStatisticsRows(ctx, counts, result)
}
