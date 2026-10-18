package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCollationDependencyClosureStableAttributes(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	d := collationUnitDefinition(e, 90000, "provider")
	ref, err := d.usage()
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
	typ := &typeDescriptor{typ: domain, namespace: d.namespace, revision: 1, owner: d.owner}
	table := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, namespace: d.namespace, name: "consumer", revision: 1, Owner: d.owner, RelationKind: tableRelation, Columns: []ColumnDescriptor{{Name: "id", Usage: integer.Usage()}, {Name: "v", Usage: usage}}, Attributes: []int32{1, 7}, AttributeHighWater: 7, Index: map[string]int{"id": 0, "v": 1}}
	for _, descriptor := range []Descriptor{d, typ, table} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := e.stageRead(stage)
	closureStage := e.BeginCatalogStage(snapshot)
	edges, err := domainDependencyClosure(context.Background(), closureStage, []ids.ObjectAddress{d.Address()})
	if err != nil {
		t.Fatal(err)
	}
	column := table.address
	column.SubID = 7
	foundColumn, foundDomain := false, false
	for _, edge := range edges {
		if edge.address == table.address {
			t.Fatal("collation cascade dropped containing table", edge)
		}
		if edge.address == column {
			foundColumn = edge.target == d.Address() && edge.root == d.Address() && edge.column == "v" && edge.ordinal == 7
		}
		if edge.address == typ.Address() {
			foundDomain = edge.target == d.Address() && edge.root == d.Address()
		}
	}
	if !foundColumn || !foundDomain {
		t.Fatal("closure lost provider or stable attribute", edges)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = domainDependencyClosure(ctx, closureStage, []ids.ObjectAddress{d.Address()}); !errors.Is(err, context.Canceled) {
		t.Fatal("closure ignored cancellation", err)
	}
}
