package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func infoTriggersInput(t *testing.T, e *core.Engine, scope *core.StatementScope) pgcatalog.Input {
	t.Helper()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	read := auth.Catalog
	source := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoTriggersOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: auth.Roles, Owner: read.Owner(), Source: source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}
