package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func partitionInfoUnitCatalog(t *testing.T, e *Engine) (catalog.Read, *sqltype.Descriptor) {
	t.Helper()
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	a := func(oid ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	}
	for _, r := range []*catalog.Relation{
		{ObjectAddress: a(41010), ObjectName: "pinfo_root", RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{Children: []ids.ObjectAddress{a(41011), a(41013)}}},
		{ObjectAddress: a(41011), ObjectName: "pinfo_mid", RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{Children: []ids.ObjectAddress{a(41012)}}, Membership: &catalog.PartitionMembership{Parent: a(41010)}},
		{ObjectAddress: a(41012), ObjectName: "pinfo_leaf", Membership: &catalog.PartitionMembership{Parent: a(41011)}},
		{ObjectAddress: a(41013), ObjectName: "pinfo_empty", RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{}, Membership: &catalog.PartitionMembership{Parent: a(41010)}},
		{ObjectAddress: a(41014), ObjectName: "pinfo_plain"},
		{ObjectAddress: a(41020), ObjectName: "pinfo_root_index", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: a(41010), Method: catalog.BtreeMethod, MethodOID: 403, Valid: false}},
		{ObjectAddress: a(41021), ObjectName: "pinfo_mid_index", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: a(41011), PartitionParent: a(41020), Method: catalog.BtreeMethod, MethodOID: 403}},
		{ObjectAddress: a(41022), ObjectName: "pinfo_leaf_index", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: a(41012), PartitionParent: a(41021), Method: catalog.BtreeMethod, MethodOID: 403}},
		{ObjectAddress: a(41023), ObjectName: "pinfo_empty_index", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: a(41013), PartitionParent: a(41020), Method: catalog.BtreeMethod, MethodOID: 403}},
		{ObjectAddress: a(41024), ObjectName: "pinfo_local_index", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: a(41012), Method: catalog.BtreeMethod, MethodOID: 403}},
	} {
		state.SetDescriptor(r.Address(), r)
	}
	view := base.WithState(state)
	regclass, _ := view.LookupType(sqltype.Regclass)
	boolean, _ := view.LookupType(sqltype.Bool)
	integer, _ := view.LookupType(sqltype.Int4)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "relid", Usage: regclass.Usage()}, {Attribute: 2, Name: "parentrelid", Usage: regclass.Usage()}, {Attribute: 3, Name: "isleaf", Usage: boolean.Usage()}, {Attribute: 4, Name: "level", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	return view, shape
}

func partitionInfoUnitTree(t *testing.T, values []sqltype.Value) map[string][]any {
	t.Helper()
	out := make(map[string][]any, len(values))
	for _, v := range values {
		id, err := v.CompositeField(0).Text()
		if err != nil {
			t.Fatal(err)
		}
		var parent any
		if !v.CompositeField(1).IsNull() {
			parent, err = v.CompositeField(1).Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		leaf, err := v.CompositeField(2).Boolean()
		if err != nil {
			t.Fatal(err)
		}
		level, err := v.CompositeField(3).Integer()
		if err != nil {
			t.Fatal(err)
		}
		if _, duplicate := out[id]; duplicate {
			t.Fatal("duplicate traversal identity", id)
		}
		out[id] = []any{parent, leaf, level}
	}
	return out
}

func TestPartitionInfoCaptureOwnership(t *testing.T) {
	e := New()
	view, shape := partitionInfoUnitCatalog(t, e)
	regclass, _ := view.LookupType(sqltype.Regclass)
	scope := tablespaceInfoUnitScope(t, e)
	scope.Inquiry.CommentCatalog = view
	host := HostOf(scope)
	old, err := host.PartitionTree(41011, shape)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	state := catalog.CloneState(view.State())
	mid, _ := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41011})
	detached := *mid
	detached.Membership = nil
	state.SetDescriptor(mid.Address(), &detached)
	scope.Inquiry.CommentCatalog = view.WithState(state)
	root, err := host.PartitionRoot(41012, regclass)
	if err != nil {
		t.Fatal(err)
	}
	text, err := root.Text()
	if err != nil || text != "41011" {
		t.Fatal("fresh root", text, err)
	}
	var retained []sqltype.Value
	for {
		v, ok, err := old.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		retained = append(retained, v)
	}
	want := map[string][]any{"41011": {"41010", false, int64(0)}, "41012": {"41011", true, int64(1)}}
	if got := partitionInfoUnitTree(t, retained); !reflect.DeepEqual(got, want) {
		t.Fatal("retained catalog changed", got)
	}
	for _, bad := range []catalog.Read{{}, New().CatalogRead()} {
		scope.Inquiry.CommentCatalog = bad
		if _, err := host.PartitionRoot(41012, regclass); err == nil {
			t.Fatal("invalid captured authority")
		}
		if _, err := host.PartitionTree(41011, shape); err == nil {
			t.Fatal("invalid captured source authority")
		}
	}
}
