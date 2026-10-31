package tsearch

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Stat is ts_stat over the documents text's query returns.
func Stat[A Authority[A]](h Host[A], ctx context.Context, text, weights string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	stat := &textSearchStat[A]{textSearchQuery: textSearchQuery[A]{host: h, text: text}, ctx: ctx, weights: weights, result: result}
	if err := textSearchSQL(h, text, true, stat); err != nil {
		return nil, err
	}
	return stat.values, nil
}

// textSearchStat consumes ts_stat's document rows.
type textSearchStat[A Authority[A]] struct {
	textSearchQuery[A]
	ctx     context.Context
	weights string
	result  *sqltype.Descriptor
	values  []sqltype.Value
}

func (t *textSearchStat[A]) NestedRows(_ parser.Statement, rows exec.RowResult) (err error) {
	if err := textSearchRows(rows); err != nil {
		return err
	}
	t.values, err = exec.ConsumeFTSStatistics(t.ctx, rows, t.weights, t.result)
	return err
}
