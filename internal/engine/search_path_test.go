package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/sqltype"
)

// Scalar unit binders use real catalog/session authorization like public SQL.
func newTestExpressionBinder(t *testing.T, ctx context.Context, e *core.Engine, input sqltype.InputEnvironment, expected *core.StatementDescription) *analyze.Binder {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return core.NewExpressionBinder(ctx, e, input, op, expected)
}
