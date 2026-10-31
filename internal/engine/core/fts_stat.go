package core

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *textSearchHost) StatTextSearch(ctx context.Context, text, weights string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	stat := &textSearchStat{textSearchQuery: textSearchQuery{host: h, spi: h.nestedSQL(), text: text}, ctx: ctx, weights: weights, result: result}
	if err := h.textSearchSQL(text, true, stat); err != nil {
		return nil, err
	}
	return stat.values, nil
}

// textSearchStat consumes ts_stat's document rows.
type textSearchStat struct {
	textSearchQuery
	ctx     context.Context
	weights string
	result  *sqltype.Descriptor
	values  []sqltype.Value
}

func (t *textSearchStat) NestedRows(_ parser.Statement, rows exec.RowResult) (err error) {
	if err := textSearchRows(rows); err != nil {
		return err
	}
	t.values, err = exec.ConsumeFTSStatistics(t.ctx, rows, t.weights, t.result)
	return err
}
