package table

import (
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestTypedSources(t *testing.T) {
	typ := func(oid ids.OID) ids.ObjectAddress { return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: oid} }
	rel := func(oid ids.OID, of ids.ObjectAddress) catalog.Descriptor {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, OfType: of}
	}
	staged := map[ids.ObjectAddress]catalog.Descriptor{}
	for _, d := range []catalog.Descriptor{rel(1, typ(30)), rel(2, ids.ObjectAddress{}), rel(3, typ(10)), rel(4, typ(30)), &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 5}}} {
		staged[d.Address()] = d
	}
	if got, want := TypedSources(false, []ids.ObjectAddress{typ(99)}, maps.All(staged)), []ids.ObjectAddress{typ(10), typ(30)}; !slices.Equal(got, want) {
		t.Fatalf("staged sources = %v, want %v", got, want)
	}
	if got, want := TypedSources(true, []ids.ObjectAddress{typ(20), typ(5), typ(20)}, maps.All(staged)), []ids.ObjectAddress{typ(5), typ(20)}; !slices.Equal(got, want) {
		t.Fatalf("dropped sources = %v, want %v", got, want)
	}
	if got := TypedSources(true, nil, maps.All(staged)); got != nil {
		t.Fatalf("no dropped sources = %v", got)
	}
	if got := TypedSources(false, nil, maps.All(map[ids.ObjectAddress]catalog.Descriptor{})); got != nil {
		t.Fatalf("no staged sources = %v", got)
	}
}
