package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func statisticsUnitEngine(t *testing.T) (*core.Engine, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	syntax := &parser.CreateTable{Name: enginetest.ConfigName("statistics_input")}
	for _, name := range []string{"a", "b", "unselected", "c"} {
		syntax.Elements = append(syntax.Elements, parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "integer"}}})
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = tablecmds.SchemaTableLocked(e, op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	enginetest.PublishOperationStage(t, e, op, stage)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "statistics_input"})
	return e, view.MustRelation(address)
}
