package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgInheritsUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2611}
	source, ok := e.catalog.state.Relation(address)
	if !ok {
		t.Fatal("missing pg_inherits")
	}
	return source, e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
}

func TestPGInheritsShapeAndOwnership(t *testing.T) {
	e := New()
	source, ref := pgInheritsUnitSource(t, e)
	view := e.CatalogRead()
	if source.Address().ObjectID != 2611 || source.Namespace().ObjectID != 11 || source.RowType != 10005 || source.ColumnMeta.HighWater != 4 || len(source.Columns) != 4 || source.VirtualView || !source.History.HasIndexes || !representedCatalogHasIndexes(2611) || pgClassKind(view, source) != "r" {
		t.Fatal("source shape", source)
	}
	names := []string{"inhrelid", "inhparent", "inhseqno", "inhdetachpending"}
	for i, id := range []sqltype.ID{26, 26, 23, 16} {
		owned, ok := view.LookupType(id)
		column := source.Columns[i]
		if !ok || column.Name != names[i] || column.Usage.Type != owned || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.Nullable || !column.NullabilityKnown || column.OriginTable != 2611 || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) || source.Index[column.Name] != i {
			t.Fatal("descriptor", i, column)
		}
	}
	if _, exists := source.Index["oid"]; exists {
		t.Fatal("invented row OID")
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10005})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10004})
	if row.OwningRelation != source.Address() || array.OwningRelation != source.Address() || row.Array != 10004 {
		t.Fatal("row/array ownership", row, array)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	captured, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner()}, capturePGInherits)
	if err != nil || captured == nil || captured.ref != ref || len(captured.anonymous) != 0 {
		t.Fatal("fresh projection", captured, err)
	}
	invalid := *source
	invalid.Columns = slices.Clone(source.Columns[:3])
	foreign := New()
	foreignSource, _ := pgInheritsUnitSource(t, foreign)
	for _, wrong := range []*catalog.Relation{&invalid, foreignSource} {
		got, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: view, Ref: ref, Source: wrong, Owner: (view).Owner()}, capturePGInherits)
		var sqlError interface{ SQLState() string }
		if got != nil || err == nil || errors.As(err, &sqlError) {
			t.Fatal("invalid source must return ordinary internal error", got, err)
		}
	}
}

// Synthetic descriptors isolate the three authoritative fields from DDL setup.
// Public reference tests independently exercise the real DDL owners.
func pgInheritsUnitGraph(t *testing.T, e *Engine) catalog.Read {
	t.Helper()
	state := *e.catalog.state
	state.CloneDescriptors()
	address := func(id ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
	}
	for _, relation := range []*catalog.Relation{
		{ObjectAddress: address(41001), ObjectName: "pi_z_parent"},
		{ObjectAddress: address(41002), ObjectName: "pi_a_parent"},
		{ObjectAddress: address(41003), ObjectName: "pi_child", Inheritance: &catalog.TableInheritance{Parents: []ids.ObjectAddress{address(41001), address(41002)}}},
		{ObjectAddress: address(41004), ObjectName: "pi_leaf", Inheritance: &catalog.TableInheritance{Parents: []ids.ObjectAddress{address(41003)}}},
		{ObjectAddress: address(41005), ObjectName: "pi_partition_root", RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{Children: []ids.ObjectAddress{address(41006)}}},
		{ObjectAddress: address(41006), ObjectName: "pi_partition", Membership: &catalog.PartitionMembership{Parent: address(41005)}},
		{ObjectAddress: address(41007), ObjectName: "pi_index_root", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: address(41005), Valid: false}},
		{ObjectAddress: address(41008), ObjectName: "pi_index_child", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: address(41006), PartitionParent: address(41007), Valid: false}},
		{ObjectAddress: address(41009), ObjectName: "pi_local_index", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: address(41003)}},
	} {
		state.SetDescriptor(relation.Address(), relation)
	}
	return e.catalog.read(&state)
}

func pgInheritsUnitEdges(t *testing.T, captured *querySource, view catalog.Read) map[[3]int64]int {
	t.Helper()
	got := make(map[[3]int64]int)
	for _, row := range captured.anonymous {
		if len(row) != 4 || cap(row) != 4 {
			t.Fatal("bounded row", len(row), cap(row))
		}
		for i, id := range []sqltype.ID{26, 26, 23, 16} {
			owned, ok := view.LookupType(id)
			if !ok || row[i].Type() != owned || row[i].IsNull() {
				t.Fatal("owned typed field", i, row[i])
			}
		}
		child, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		parent, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		ordinal, err := row[2].Integer()
		if err != nil {
			t.Fatal(err)
		}
		pending, err := row[3].Boolean()
		if err != nil || pending {
			t.Fatal("pending", pending, err)
		}
		got[[3]int64{int64(child), int64(parent), ordinal}]++
	}
	return got
}

func TestPGInheritsDescriptorEdges(t *testing.T) {
	e := New()
	source, ref := pgInheritsUnitSource(t, e)
	view := pgInheritsUnitGraph(t, e)
	captured, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner()}, capturePGInherits)
	if err != nil {
		t.Fatal(err)
	}
	want := map[[3]int64]int{{41003, 41001, 1}: 1, {41003, 41002, 2}: 1, {41004, 41003, 1}: 1, {41006, 41005, 1}: 1, {41008, 41007, 1}: 1}
	if got := pgInheritsUnitEdges(t, captured, view); !reflect.DeepEqual(got, want) {
		t.Fatal("direct edge bag", got, want)
	}
	// Publish a distinct fixture generation without altering the retained read.
	next := *view.State()
	next.CloneDescriptors()
	next.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41008})
	child := *next.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41003})
	child.Inheritance = nil
	next.SetDescriptor(child.Address(), &child)
	e.catalog.state = &next
	current, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: source, Owner: (e.CatalogRead()).Owner()}, capturePGInherits)
	if err != nil {
		t.Fatal(err)
	}
	remaining := map[[3]int64]int{{41004, 41003, 1}: 1, {41006, 41005, 1}: 1}
	if got := pgInheritsUnitEdges(t, current, e.CatalogRead()); !reflect.DeepEqual(got, remaining) {
		t.Fatal("new generation", got, remaining)
	}
	again, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner()}, capturePGInherits)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []*querySource{captured, again} {
		if got := pgInheritsUnitEdges(t, old, view); !reflect.DeepEqual(got, want) {
			t.Fatal("retained generation changed", got, want)
		}
	}
}

func TestPGInheritsCancellation(t *testing.T) {
	e := New()
	source, ref := pgInheritsUnitSource(t, e)
	view := pgInheritsUnitGraph(t, e)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{
		canceled,
		&pgEnumCancelDuringCapture{Context: context.Background(), after: 2},
		&pgEnumCancelDuringCapture{Context: context.Background(), after: view.DescriptorCount() + 2},
	} {
		if got, err := captureCatalogSourceRows(catalogSourceInput{Context: ctx, Catalog: view, Ref: ref, Source: source, Owner: (view).Owner()}, capturePGInherits); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial capture published", got, err)
		}
	}
}
