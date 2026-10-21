package dump

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Resolver maps a dependency address to the OID of the dump event that must
// precede consumer, or 0 when nothing must. The engine implements it over
// partition, operator and index helpers that live above this package.
type Resolver interface {
	Predecessor(view catalog.Read, address, consumer ids.ObjectAddress) (ids.OID, error)
}

// Presenter renders catalog values for dump statements. Every method is a
// pure function of its arguments; implementations read no engine state and
// take no lock. Methods are added with their first caller.
type Presenter interface {
	TypeDeclaration(view catalog.Read, reader catalog.DefinitionReader, u sqltype.Usage) (string, error)
	ColumnDeclaration(view catalog.Read, column catalog.Column) (string, error)
	RoutineIdentity(view catalog.Read, oid ids.OID) (catalog.RoutineIdentity, bool)
	// DomainDefault renders a domain's own DEFAULT expression in the live
	// row shape that replay executes.
	DomainDefault(ctx context.Context, view catalog.Read, node expr.Expr) (string, error)
	// DomainCheck renders a domain CHECK expression, with value as the
	// type of VALUE.
	DomainCheck(ctx context.Context, view catalog.Read, value sqltype.Usage, node expr.Expr) (string, error)
}
