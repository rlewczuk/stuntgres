package stuntgres

import (
	"context"

	"stuntgres/internal/util"
)

// ExecutionPause is a one-shot cooperative suspension at a supported execution
// work point. It is safe for concurrent use and never owns cancellation.
// See docs/DRIVER.md for supported points and lifecycle requirements.
type ExecutionPause = util.ExecutionPause

// NewExecutionPause requests suspension when work reaches point. Creating the
// pause does not establish admission; Wait observes actual matching work.
func NewExecutionPause(point string) *ExecutionPause { return util.NewExecutionPause(point) }

// WithExecutionPause scopes a pause to work using ctx and its descendants.
// It preserves the context's values, cancellation, cause and deadline. A nested
// call replaces the inherited pause; nil removes it. Always Resume on cleanup.
func WithExecutionPause(ctx context.Context, pause *ExecutionPause) context.Context {
	return util.WithExecutionPause(ctx, pause)
}
