package engine

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestRegtypeInitializedCatalogAuthority(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	count := 0
	for address, descriptor := range view.state.descriptors {
		typ, ok := descriptor.(*typeDescriptor)
		if !ok {
			continue
		}
		count++
		if err := (typeProvider{}).Validate(view, typ); err != nil {
			t.Fatal(address, err)
		}
		if typ.array != 0 {
			array, ok := view.LookupArray(typ.typ)
			if !ok || array.Element() != typ.typ {
				t.Fatal("nonreciprocal catalog array", address)
			}
		}
	}
	if count != 621 {
		t.Fatal("initialized inventory", count)
	}
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: 13229}
	if d, ok := view.Descriptor(ns); !ok || d.Name() != "information_schema" {
		t.Fatal("initialized namespace", d, ok)
	}
	for _, name := range []string{"pg_type", "pg_class", "pg_index", "pg_sequence", "pg_attribute"} {
		address := view.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, Name: name}]
		relation, exists := view.state.descriptors[address].(*relationDescriptor)
		if name == "pg_index" && !exists {
			t.Fatal("missing represented pg_index projection")
		}
		if !exists {
			// Lookup-only identities do not create executable projections.
			types := view.Lookup(NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, Name: name})
			if len(types) != 1 {
				t.Fatal("lookup-only row identity", name)
			}
			row := view.state.descriptors[types[0]].(*typeDescriptor)
			if row.typ.Concrete() || row.owningRelation != (ObjectAddress{}) {
				t.Fatal("lookup-only identity implies relation execution", name)
			}
			continue
		}
		row := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
		if row.owningRelation != address || row.Name() != relation.Name() || row.typ.Concrete() {
			t.Fatal("projection row authority", name)
		}
	}
}

func TestRegtypeRecursiveClassRemapping(t *testing.T) {
	e := New()
	scalar, _ := e.CatalogRead().LookupType(sqltype.Regtype)
	domain, err := sqltype.Domain(30000, "identifier", "public.identifier", scalar.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(30001, "_identifier", domain)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := sqltype.RegtypeValue(scalar, 23)
	wrapped, err := domain.Wrap(value)
	if err != nil {
		t.Fatal(err)
	}
	values, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{wrapped, sqltype.Null(domain)})
	if err != nil {
		t.Fatal(err)
	}
	literal := literalExpression{expressionInfo: expressionInfo{usage: array.Usage()}, value: values}
	mapping := map[ObjectAddress]ObjectAddress{
		{ClassID: RelationClassID, ObjectID: 23}: {ClassID: RelationClassID, ObjectID: 99},
	}
	transform := func(ref ObjectRef) ObjectRef {
		if target, ok := mapping[ref.Address]; ok {
			ref.Address = target
		}
		return ref
	}
	untouched, err := remapBoundExpression(literal, transform, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := untouched.(literalExpression).value.ArrayElement(0).TypeOID()
	if err != nil || id != 23 {
		t.Fatal("relation ID used as type identity", id, err)
	}
	mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: 23}] = ObjectAddress{ClassID: TypeClassID, ObjectID: 42}
	changed, err := remapBoundExpression(literal, transform, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := changed.(literalExpression).value
	id, err = next.ArrayElement(0).TypeOID()
	if err != nil || id != 42 || !next.ArrayElement(1).IsNull() || next.ArrayElement(0).Type() != domain {
		t.Fatal("recursive remap", id, err)
	}
	old, err := values.ArrayElement(0).TypeOID()
	if err != nil || old != 23 {
		t.Fatal("source payload mutated", old, err)
	}
}

func TestRegtypeOIDAllocationSkipsTypeIdentities(t *testing.T) {
	e := New()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextOID = OID(sqltype.Regtype)
	id, err := e.allocateOIDLocked()
	if err != nil {
		t.Fatal(err)
	}
	for address := range e.state.descriptors {
		if address.ObjectID == id {
			t.Fatal("allocated published identity", address)
		}
	}
}
