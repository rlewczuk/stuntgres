package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTypeSpellingPortableIdentity(t *testing.T) {
	state := NewState(1)
	pg := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "pg_catalog"}
	odd := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 91000}, ObjectName: "Odd Namespace"}
	for _, namespace := range []*Namespace{pg, odd} {
		state.SetDescriptor(namespace.Address(), namespace)
		state.BindName(DescriptorNameKey(namespace), namespace.Address())
	}
	for _, typ := range sqltype.Builtins() {
		descriptor := &Type{SQLType: typ}
		state.SetDescriptor(descriptor.Address(), descriptor)
		state.BindName(DescriptorNameKey(descriptor), descriptor.Address())
	}
	typ := &Type{SQLType: sqltype.LookupOnly(91001, "select", 91002), ObjectNamespace: odd.Address()}
	array := &Type{SQLType: sqltype.LookupArray(91002, "_select", typ.SQLType), ObjectNamespace: odd.Address()}
	for _, descriptor := range []*Type{typ, array} {
		state.SetDescriptor(descriptor.Address(), descriptor)
		state.BindName(DescriptorNameKey(descriptor), descriptor.Address())
	}
	view := Read{state: state}
	for _, test := range []struct {
		id   ids.OID
		path []ids.ObjectAddress
		want string
	}{
		{ids.OID(sqltype.Int4), nil, "integer"},
		{ids.OID(sqltype.Bool), nil, "boolean"},
		{ids.OID(sqltype.JSON), nil, "json"},
		{ids.OID(sqltype.Text), nil, "pg_catalog.text"},
		{22, nil, "pg_catalog.int2vector"},
		{typ.Address().ObjectID, nil, `"Odd Namespace"."select"`},
		{array.Address().ObjectID, nil, `"Odd Namespace"."select"[]`},
		{array.Address().ObjectID, []ids.ObjectAddress{odd.Address()}, `"select"[]`},
		{0, nil, "-"},
		{999999, nil, "999999"},
	} {
		if got := TypeSpelling(view, test.path, test.id); got != test.want {
			t.Errorf("id=%d path=%v got=%q want=%q", test.id, test.path, got, test.want)
		}
	}
	if got := QualifiedTypeSpelling(view, array.Address().ObjectID); got != `"Odd Namespace"."select"[]` {
		t.Fatal("machine identity changed canonical spelling", got)
	}
}

func TestNamedObjectVisibleAtStagedDefinitions(t *testing.T) {
	state := NewState(1)
	view := Read{state: state}
	first := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 92000}, ObjectName: "first"}
	second := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 92001}, ObjectName: "second"}
	typ := &Type{SQLType: sqltype.LookupOnly(92002, "value", 0), ObjectNamespace: second.Address()}
	shadow := &Type{SQLType: sqltype.LookupOnly(92003, "value", 0), ObjectNamespace: first.Address()}
	candidate := NewCandidate(view)
	for _, descriptor := range []Descriptor{first, second, typ, shadow} {
		if err := candidate.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	reader := candidate.Definitions()
	if NamedObjectVisibleAt(view, reader, nil, typ) || !NamedObjectVisibleAt(view, reader, []ids.ObjectAddress{second.Address()}, typ) || NamedObjectVisibleAt(view, reader, []ids.ObjectAddress{first.Address(), second.Address()}, typ) {
		t.Fatal("exact staged name selection changed")
	}
	if _, exists := view.Descriptor(typ.Address()); exists {
		t.Fatal("visibility lookup published a candidate")
	}
}
