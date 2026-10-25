package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
)

// ScopeEnlistForeign calls x.enlistForeign.
func ScopeEnlistForeign(x *StatementScope, ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error) {
	return x.enlistForeign(ctx, wrapper, request, writing)
}

// ScopeForeignTablesOrZero calls x.foreignTablesOrZero.
func ScopeForeignTablesOrZero(x *StatementScope) foreignTableScope {
	return x.foreignTablesOrZero()
}

// ForeignTableScopeCursor returns x.cursor.
func ForeignTableScopeCursor(x foreignTableScope) *sqlCursor { return x.cursor }
