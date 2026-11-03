package tsearch

import (
	"context"
	"stuntgres/internal/sqltype"
)

// Headline captures the execution-live configuration once, normalizes detached
// through the shared rich walk, and fences all consulted metadata/receipts before
// publishing owned TEXT. Parser capability precedes parsing and option demand.
func Headline[A Authority[A]](h Host[A], ctx context.Context, explicit bool, configuration, document, query, options sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	captured, err := captureTextSearchInput(h, ctx, explicit, configuration, false)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx = h.ActivateRuntime(ctx)
	if err := captured.parser.CheckHeadline(ctx); err != nil {
		return sqltype.Value{}, err
	}
	text, err := document.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	raw, err := captured.WalkDocument(ctx, h, text)
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := sqltype.FTSHeadlineResult(ctx, result, raw, query, options)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := captured.Accept(ctx, h); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
