package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func collationUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func collationUnitDefinition(e *Engine, id ids.OID, name string) *catalog.Collation {
	owner, _ := e.Roles().Lookup("postgres")
	return &catalog.Collation{OID: id, ObjectName: name, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, Owner: owner.Ref, Provider: "c", Deterministic: true, Encoding: -1, Collate: catalog.CollationText("C"), CType: catalog.CollationText("C"), Semantics: 950}
}
