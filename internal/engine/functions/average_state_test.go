package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// This test-only interface exercises the same existing aggregate method set.
// Production providers satisfy the engine's structural interface directly.
type numericProviderUnit interface {
	ResultUsage() sqltype.Usage
	Init(context.Context) (any, error)
	Advance(context.Context, any, []sqltype.Value, expr.Context) error
	Finalize(context.Context, any) (sqltype.Value, error)
}

func numericUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}
