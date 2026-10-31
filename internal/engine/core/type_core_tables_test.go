package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTypeCoreCatalogRejectsNoncurrentColumnTypes(t *testing.T) {
	e := engine.New()
	stale := enginetest.Usage(e, sqltype.Int4)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []sqltype.Usage{{}, enginetest.Usage(e, sqltype.Unknown), enginetest.Usage(engine.New(), sqltype.Int4), stale} {
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		descriptor := &catalog.Relation{
			ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000},
			ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
			ObjectName:      "invalid", ObjectRevision: 1,
			Columns: []catalog.Column{{Name: "id", Usage: usage}},
			Index:   map[string]int{"id": 0}, Attributes: []int32{1},
			ColumnMeta: catalog.ColumnMetadata{
				HighWater: 1,
			},
		}
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
			t.Fatal("invalid column type accepted", usage)
		}
		if e.CatalogRead().State() != base.State() {
			t.Fatal("invalid type published")
		}
	}
}
