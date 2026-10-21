package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// dumpPresenter is the root's dump.Presenter. Each method forwards to a pure
// presentation helper that reads only its arguments.
type dumpPresenter struct{}

var _ dump.Presenter = dumpPresenter{}

func (dumpPresenter) TypeDeclaration(view catalog.Read, reader catalog.DefinitionReader, u sqltype.Usage) (string, error) {
	return regobj.TypeDeclarationAt(view, reader, u)
}

func (dumpPresenter) ColumnDeclaration(view catalog.Read, column catalog.Column) (string, error) {
	return regobj.ColumnDeclaration(view, column)
}

func (dumpPresenter) RoutineIdentity(view catalog.Read, oid ids.OID) (catalog.RoutineIdentity, bool) {
	return regobj.RoutineIdentityByOID(view, oid)
}

func (dumpPresenter) DomainDefault(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	return (scalarRenderer{currentRowShape: true}).sql(ctx, view, node)
}

func (dumpPresenter) DomainCheck(ctx context.Context, view catalog.Read, value sqltype.Usage, node expr.Expr) (string, error) {
	return (scalarRenderer{domainValue: &value}).sql(ctx, view, node)
}
