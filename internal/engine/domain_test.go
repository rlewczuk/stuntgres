package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These fixtures exercise private staging/publication, never SQL execution.
func domainUnitStage(t *testing.T, e *core.Engine, op *session.Operation, q string) *core.CatalogStage {
	t.Helper()
	ast := statement(t, q)
	e.Mu.Lock()
	var stage *core.CatalogStage
	var err error
	if enum, ok := ast.(*parser.CreateEnum); ok {
		var auth core.AuthorizationView
		auth, err = e.AuthorizationLocked(op)
		if err == nil {
			stage = e.BeginCatalogStage(auth.Catalog)
			err = typedef.StageCreateEnumLocked(tablecmds.TypeHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, enum)
		}
	} else {
		stage, err = schemacmds.StageSchemaLocked(e, op, ast, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return stage
}

func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
