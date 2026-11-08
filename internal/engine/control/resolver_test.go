package control

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
)

func TestRoutineDumpConstraintPredecessor(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	parent := &catalog.Relation{
		ObjectAddress:   fdwUnitAddress(ids.RelationClassID, 940100),
		ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID),
		ObjectName:      "group_parent",
		RelationKind:    catalog.TableRelation,
	}
	backing := &catalog.Relation{
		ObjectAddress:   fdwUnitAddress(ids.RelationClassID, 940104),
		ObjectNamespace: parent.Namespace(),
		ObjectName:      "group_parent_pkey",
		RelationKind:    catalog.IndexRelation,
		SQLIndex:        &catalog.IndexDefinition{Parent: parent.Address(), Unique: true},
	}
	parent.Constraints.Unique = []catalog.UniqueConstraint{{OID: 940101, Name: "group_parent_pkey", Primary: true, Attributes: []int32{1}, Index: backing.Address()}}
	backing.SQLIndex.Constraint = index.UniqueBacking(&parent.Constraints.Unique[0])
	source.State.SetDescriptor(backing.Address(), backing)
	source.State.SetDescriptor(parent.Address(), parent)
	constraint := fdwUnitAddress(2606, 940101)
	consumer := &catalog.Procedure{
		ObjectAddress:           fdwUnitAddress(ids.ProcedureClassID, 940102),
		DeclarationDependencies: []ids.ObjectAddress{constraint},
	}
	graph := dump.Graph{}
	if err := routine.RoutineDumpEvents(Resolver{}, source.CatalogView, []*catalog.Procedure{consumer}, graph); err != nil {
		t.Fatal(err)
	}
	edges := graph[dump.Event{OID: consumer.Address().ObjectID}]
	if len(edges) != 1 || edges[0] != (dump.Event{OID: parent.Address().ObjectID}) {
		t.Fatalf("attached constraint predecessors = %v, want genuine parent", edges)
	}
	if _, exists := source.CatalogView.Descriptor(constraint); exists {
		t.Fatal("attached constraint acquired a descriptor")
	}
	if consumer.DeclarationDependencies[0] != constraint {
		t.Fatal("replay changed the retained constraint identity")
	}
	if oid, err := (Resolver{}).Predecessor(source.CatalogView, constraint, parent.Address()); err != nil || oid != 0 {
		t.Fatalf("parent self predecessor = %d, %v", oid, err)
	}
	column := parent.Address()
	column.SubID = 1
	if oid, err := (Resolver{}).Predecessor(source.CatalogView, column, consumer.Address()); err != nil || oid != parent.Address().ObjectID {
		t.Fatalf("column predecessor = %d, %v", oid, err)
	}
	separate := *backing
	definition := *backing.SQLIndex
	definition.Include = []int32{2}
	separate.SQLIndex = &definition
	source.State.SetDescriptor(separate.Address(), &separate)
	separateGraph := dump.Graph{}
	if err := routine.RoutineDumpEvents(Resolver{}, source.CatalogView, []*catalog.Procedure{consumer}, separateGraph); err != nil {
		t.Fatal(err)
	}
	separateEdges := separateGraph[dump.Event{OID: consumer.Address().ObjectID}]
	if len(separateEdges) != 1 || separateEdges[0] != (dump.Event{OID: backing.Address().ObjectID}) {
		t.Fatalf("separate constraint predecessors = %v, want supporting index", separateEdges)
	}
	if oid, err := (Resolver{}).Predecessor(source.CatalogView, constraint, parent.Address()); err != nil || oid != backing.Address().ObjectID {
		t.Fatalf("separate attachment parent predecessor = %d, %v", oid, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*catalog.Relation)
	}{
		{"wrong-kind", func(r *catalog.Relation) { r.RelationKind = catalog.TableRelation }},
		{"missing-definition", func(r *catalog.Relation) { r.SQLIndex = nil }},
		{"foreign-parent", func(r *catalog.Relation) { r.SQLIndex.Parent.ObjectID++ }},
		{"foreign-namespace", func(r *catalog.Relation) { r.ObjectNamespace.ObjectID++ }},
		{"foreign-owner", func(r *catalog.Relation) { r.Owner = source.Bootstrap }},
		{"orphan", func(r *catalog.Relation) { r.SQLIndex.Constraint = nil }},
		{"foreign-constraint", func(r *catalog.Relation) { r.SQLIndex.Constraint.OID++ }},
		{"foreign-index", func(r *catalog.Relation) { r.SQLIndex.Constraint.Index.ObjectID++ }},
		{"foreign-family", func(r *catalog.Relation) { r.SQLIndex.Constraint.Kind = catalog.ExclusionBackingKind }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := separate
			badDefinition := definition
			badConstraint := *definition.Constraint
			badDefinition.Constraint = &badConstraint
			bad.SQLIndex = &badDefinition
			tc.mutate(&bad)
			source.State.SetDescriptor(backing.Address(), &bad)
			if _, err := (Resolver{}).Predecessor(source.CatalogView, constraint, consumer.Address()); err == nil {
				t.Fatal("invalid supporting index accepted")
			}
			source.State.SetDescriptor(backing.Address(), &separate)
		})
	}
	for _, invalid := range []ids.ObjectAddress{
		{},
		fdwUnitAddress(ids.RelationClassID, 940199),
		fdwUnitAddress(ids.TypeClassID, backing.Address().ObjectID),
		{ClassID: ids.RelationClassID, ObjectID: backing.Address().ObjectID, SubID: 1},
	} {
		bad := *parent
		bad.Constraints.Unique = append([]catalog.UniqueConstraint(nil), parent.Constraints.Unique...)
		bad.Constraints.Unique[0].Index = invalid
		source.State.SetDescriptor(parent.Address(), &bad)
		if _, err := (Resolver{}).Predecessor(source.CatalogView, constraint, consumer.Address()); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatalf("invalid backing identity %v: %v, want catalog replacement", invalid, err)
		}
		source.State.SetDescriptor(parent.Address(), parent)
	}
	for _, invalid := range []ids.ObjectAddress{
		{ClassID: 2606},
		{ClassID: 2606, ObjectID: 940103},
		{ClassID: 2606, ObjectID: constraint.ObjectID, SubID: 1},
	} {
		consumer.DeclarationDependencies = []ids.ObjectAddress{invalid}
		if err := routine.RoutineDumpEvents(Resolver{}, source.CatalogView, []*catalog.Procedure{consumer}, dump.Graph{}); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatalf("invalid constraint %v: %v, want catalog replacement", invalid, err)
		}
	}
	changed := *parent
	changed.Constraints.Unique = nil
	source.State.SetDescriptor(changed.Address(), &changed)
	if _, err := (Resolver{}).Predecessor(source.CatalogView, constraint, consumer.Address()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("retired constraint: %v, want catalog replacement", err)
	}
}
