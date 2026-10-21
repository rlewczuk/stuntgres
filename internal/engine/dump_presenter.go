package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// dumpPresenter is the root's dump.Presenter. Each method forwards to a pure
// presentation helper that reads only its arguments.
type dumpPresenter struct{}

var _ dump.Presenter = dumpPresenter{}

func (dumpPresenter) TypeDeclaration(view catalog.Read, reader catalog.DefinitionReader, u sqltype.Usage) (string, error) {
	return catalogTypeDeclarationAt(view, reader, u)
}

func (dumpPresenter) ColumnDeclaration(view catalog.Read, column catalog.Column) (string, error) {
	return catalogColumnDeclaration(view, column)
}

func (dumpPresenter) RoutineIdentity(view catalog.Read, oid ids.OID) (catalog.RoutineIdentity, bool) {
	return routineIdentityByOID(view, oid)
}
