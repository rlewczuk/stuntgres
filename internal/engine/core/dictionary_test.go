package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func dictionaryUnitPrepare(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement) (*core.CatalogStage, extension.DictionaryPreparation) {
	t.Helper()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	p, err := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Compile(op.Context(), core.DDLHost{E: e, Op: op, CatalogStage: stage}); err != nil {
		t.Fatal(err)
	}
	return stage, p
}
