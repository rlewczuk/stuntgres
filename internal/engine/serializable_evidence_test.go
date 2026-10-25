package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func serializableUnitRelation(t *testing.T, e *core.Engine) ids.ObjectRef {
	t.Helper()
	base := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}
	d := &catalog.Relation{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "ssi_unit", ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "v", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}
