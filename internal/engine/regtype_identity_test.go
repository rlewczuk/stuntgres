package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRegtypeInitializedCatalogAuthority(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	count := 0
	for address, descriptor := range view.Descriptors() {
		typ, ok := descriptor.(*catalog.Type)
		if !ok {
			continue
		}
		count++
		if err := (stored.TypeProvider{}).Validate(view, typ); err != nil {
			t.Fatal(address, err)
		}
		if typ.Array != 0 {
			array, ok := view.LookupArray(typ.SQLType)
			if !ok || array.Element() != typ.SQLType {
				t.Fatal("nonreciprocal catalog array", address)
			}
		}
	}
	if count != 621 {
		t.Fatal("initialized inventory", count)
	}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}
	if d, ok := view.Descriptor(ns); !ok || d.Name() != "information_schema" {
		t.Fatal("initialized namespace", d, ok)
	}
	for _, name := range []string{"pg_type", "pg_class", "pg_index", "pg_sequence", "pg_attribute"} {
		address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: name})
		relation, exists := view.Relation(address)
		if name == "pg_index" && !exists {
			t.Fatal("missing represented pg_index projection")
		}
		if !exists {
			// Lookup-only identities do not create executable projections.
			types := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: name})
			if len(types) != 1 {
				t.Fatal("lookup-only row identity", name)
			}
			row := view.MustType(types[0])
			if row.SQLType.Concrete() || row.OwningRelation != (ids.ObjectAddress{}) {
				t.Fatal("lookup-only identity implies relation execution", name)
			}
			continue
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
		if row.OwningRelation != address || row.Name() != relation.Name() || row.SQLType.Concrete() {
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
	literal := plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: values}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		{ClassID: ids.RelationClassID, ObjectID: 23}: {ClassID: ids.RelationClassID, ObjectID: 99},
	}
	transform := func(ref ids.ObjectRef) ids.ObjectRef {
		if target, ok := mapping[ref.Address]; ok {
			ref.Address = target
		}
		return ref
	}
	untouched, err := stored.RemapExpression(literal, transform, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := untouched.(plan.LiteralExpr).Value.ArrayElement(0).TypeOID()
	if err != nil || id != 23 {
		t.Fatal("relation ID used as type identity", id, err)
	}
	mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 42}
	changed, err := stored.RemapExpression(literal, transform, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := changed.(plan.LiteralExpr).Value
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
	e.Mu.Lock()
	defer e.Mu.Unlock()
	e.Catalog.InstallOIDFrontierLocked(ids.OID(sqltype.Regtype))
	id, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		t.Fatal(err)
	}
	for address := range e.Catalog.State.Descriptors() {
		if address.ObjectID == id {
			t.Fatal("allocated published identity", address)
		}
	}
}
