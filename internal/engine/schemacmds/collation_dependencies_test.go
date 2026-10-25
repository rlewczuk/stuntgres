package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestCollationDependencyClosureStableAttributes(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	d := collationUnitDefinition(e, 90000, "provider")
	ref, err := d.Usage()
	if err != nil {
		t.Fatal(err)
	}
	text, _ := base.LookupType(sqltype.Text)
	integer, _ := base.LookupType(sqltype.Int4)
	usage := text.Usage()
	usage.Collation = ref
	domain, err := sqltype.Domain(90001, "registered_domain", "REGISTERED_DOMAIN", usage)
	if err != nil {
		t.Fatal(err)
	}
	typ := &catalog.Type{SQLType: domain, ObjectNamespace: d.ObjectNamespace, ObjectRevision: 1, Owner: d.Owner}
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, ObjectNamespace: d.ObjectNamespace, ObjectName: "consumer", ObjectRevision: 1, Owner: d.Owner, RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage()}, {Name: "v", Usage: usage}}, Attributes: []int32{1, 7}, ColumnMeta: catalog.ColumnMetadata{HighWater: 7}, Index: map[string]int{"id": 0, "v": 1}}
	for _, descriptor := range []catalog.Descriptor{d, typ, table} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := e.StageRead(stage)
	closureStage := e.BeginCatalogStage(snapshot)
	edges, err := engine.DomainDependencyClosure(context.Background(), closureStage, []ids.ObjectAddress{d.Address()})
	if err != nil {
		t.Fatal(err)
	}
	column := table.Address()
	column.SubID = 7
	foundColumn, foundDomain := false, false
	for _, edge := range edges {
		if edge.Address == table.Address() {
			t.Fatal("collation cascade dropped containing table", edge)
		}
		if edge.Address == column {
			foundColumn = edge.Target == d.Address() && edge.Root == d.Address() && edge.Column == "v" && edge.Ordinal == 7
		}
		if edge.Address == typ.Address() {
			foundDomain = edge.Target == d.Address() && edge.Root == d.Address()
		}
	}
	if !foundColumn || !foundDomain {
		t.Fatal("closure lost provider or stable attribute", edges)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = engine.DomainDependencyClosure(ctx, closureStage, []ids.ObjectAddress{d.Address()}); !errors.Is(err, context.Canceled) {
		t.Fatal("closure ignored cancellation", err)
	}
}
