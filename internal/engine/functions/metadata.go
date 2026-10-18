// Package functions owns builtin-specific analysis, immutable calls and aggregate states.
package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

func evaluationContext(c expr.Context) (context.Context, error) {
	ctx := c.FunctionContext()
	if ctx == nil {
		return nil, fmt.Errorf("stuntgres: missing expression context")
	}
	return ctx, ctx.Err()
}
func sqlError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

type Resolution uint8

const (
	Resolved Resolution = iota
	Missing
	Ambiguous
	UnknownPolymorphic
)
