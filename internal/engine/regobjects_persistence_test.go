package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestRegObjectAliasCapturedAuthority(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	r := capturedRegObjectInput{auth: AuthorizationView{catalog: view}, path: []ids.ObjectAddress{catalog}}
	if id, err := r.ResolveRegnamespace(context.Background(), " PG_CATALOG "); err != nil || id != 11 {
		t.Fatal("namespace catalog lookup", id, err)
	}
	if id, err := r.ResolveRegcollation(context.Background(), `"C"`); err != nil || id != 950 {
		t.Fatal("collation catalog lookup", id, err)
	}
	collation, _ := view.LookupType(sqltype.Regcollation)
	namespace, _ := view.LookupType(sqltype.Regnamespace)
	rows := &finalRows{}
	// Equal numeric payloads in different catalogs must not share presentation.
	c, _ := sqltype.RegObjectValue(collation, 950)
	n, _ := sqltype.RegObjectValue(namespace, 950)
	for _, value := range []sqltype.Value{c, n} {
		if err := captureRegObjectOutputValue(rows, view, r.path, value); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := rows.FormatRegcollation(950); err != nil || got != `"C"` {
		t.Fatal("collation output", got, err)
	}
	if got, err := rows.FormatRegnamespace(950); err != nil || got != "950" {
		t.Fatal("namespace output collision", got, err)
	}
	fallback := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 80000}
	exact := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 80001}
	if got := selectCollation([]collationCandidate{{fallback, -1}, {exact, 6}}, 6); got != exact {
		t.Fatal("exact encoding preference", got)
	}
	if got := selectCollation([]collationCandidate{{fallback, -1}, {exact, 8}}, 6); got != fallback {
		t.Fatal("encoding fallback", got)
	}
}

func TestRegObjectAliasPersistence(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, fact := range []struct {
		typ   sqltype.ID
		oid   uint32
		class ids.OID
	}{{sqltype.Regcollation, 950, ids.CollationClassID}, {sqltype.Regnamespace, 11, ids.NamespaceClassID}} {
		typ, _ := view.LookupType(fact.typ)
		value, err := sqltype.RegObjectValue(typ, fact.oid)
		if err != nil {
			t.Fatal(err)
		}
		var references []ids.ObjectAddress
		if err := walkCatalogAliasReferences(view, value, func(address ids.ObjectAddress) error { references = append(references, address); return nil }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(references, []ids.ObjectAddress{{ClassID: fact.class, ObjectID: ids.OID(fact.oid)}}) {
			t.Fatal("class-aware references", references)
		}
		references = nil
		if err := walkCatalogAliasDependencies(view, value, func(address ids.ObjectAddress) error { references = append(references, address); return nil }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(references, []ids.ObjectAddress{{ClassID: fact.class, ObjectID: ids.OID(fact.oid)}}) {
			t.Fatal("scalar alias dependency", references)
		}
		arrayType, _ := view.LookupType(typ.AssociatedArray())
		array, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: -2, Length: 3}}, []sqltype.Value{value, sqltype.Null(typ), value})
		if err != nil {
			t.Fatal(err)
		}
		domain, err := sqltype.Domain(80000+fact.typ, "alias_domain", "alias_domain", arrayType.Usage())
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := domain.Wrap(array)
		if err != nil {
			t.Fatal(err)
		}
		for _, opaque := range []sqltype.Value{array, wrapped} {
			references = nil
			if err := walkCatalogAliasDependencies(view, opaque, func(address ids.ObjectAddress) error { references = append(references, address); return nil }); err != nil || len(references) != 0 {
				t.Fatal("opaque alias payload gained dependency", references, err)
			}
		}
		scalarDomain, err := sqltype.Domain(90000+fact.typ, "scalar_alias_domain", "scalar_alias_domain", typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		scalarWrapped, err := scalarDomain.Wrap(value)
		if err != nil {
			t.Fatal(err)
		}
		references = nil
		if err := walkCatalogAliasDependencies(view, scalarWrapped, func(address ids.ObjectAddress) error { references = append(references, address); return nil }); err != nil || len(references) != 0 {
			t.Fatal("domain Const mistaken for scalar alias Const", references, err)
		}
		remapped, err := remapCatalogAliasDatum(wrapped, func(address ids.ObjectAddress) ids.ObjectAddress {
			if address.ClassID == fact.class && address.ObjectID == ids.OID(fact.oid) {
				address.ObjectID++
			}
			return address
		})
		if err != nil || remapped.Type() != domain || !remapped.ArrayElement(1).IsNull() {
			t.Fatal("domain/array remap", remapped, err)
		}
		id, err := remapped.ArrayElement(0).ObjectOID()
		if err != nil || id != fact.oid+1 {
			t.Fatal("class-aware datum remap", id, err)
		}
		unknown, _ := sqltype.RegObjectValue(typ, 4294967295)
		references = nil
		if err := walkCatalogAliasReferences(view, unknown, func(address ids.ObjectAddress) error { references = append(references, address); return nil }); err != nil || len(references) != 0 {
			t.Fatal("unknown OID acquired dependency", references, err)
		}
		wrongClass := ids.TypeClassID
		unchanged, err := remapCatalogAliasDatum(value, func(address ids.ObjectAddress) ids.ObjectAddress {
			if address.ClassID == wrongClass {
				address.ObjectID++
			}
			return address
		})
		if err != nil || unchanged != value {
			t.Fatal("cross-catalog numeric remap", unchanged, err)
		}
	}
}

func TestRegObjectAliasComparisonBoundary(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Regcollation)
	value, err := sqltype.RegObjectValue(typ, 963)
	if err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 963})
	literal := literalExpression{expressionInfo: expressionInfo{Usage: typ.Usage()}, value: value}
	if err := validateCollationProvider(view, ref); err == nil {
		t.Fatal("fixture's unsupported comparison provider became executable")
	}
	if err := validateStoredCollationDependency(view, ref, literal); err != nil {
		t.Fatal("scalar collation identity requires comparison capability", err)
	}
	// Even the very same alias Const cannot authorize an actual collation use.
	literal.Usage.Collation.ID = 963
	if err := validateStoredCollationDependency(view, ref, literal); err == nil {
		t.Fatal("identity reference bypassed comparison validation")
	}
	arrayType, _ := view.LookupType(typ.AssociatedArray())
	array, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{value})
	if err != nil {
		t.Fatal(err)
	}
	opaque := literalExpression{expressionInfo: expressionInfo{Usage: arrayType.Usage()}, value: array}
	if err := validateStoredCollationDependency(view, ref, opaque); err == nil {
		t.Fatal("opaque array mistaken for scalar collation identity")
	}
	ref.Address.SubID = 1
	if err := validateStoredCollationDependency(view, ref, literal); err == nil {
		t.Fatal("attribute address accepted as collation identity")
	}
}
