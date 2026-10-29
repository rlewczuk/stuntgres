package core

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) StatTextSearch(ctx context.Context, text, weights string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	var values []sqltype.Value
	err := h.scope().consumeTextSearchSQL(text, true, func(rows exec.RowCursor) error {
		var err error
		values, err = exec.ConsumeFTSStatistics(ctx, rows, weights, result)
		return err
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}
