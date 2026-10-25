package engine

import (
	"testing"

	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCompositeArrayStorageAdmission(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	row, ok := view.LookupType(source.RowType)
	if !ok {
		t.Fatal("missing composite codec")
	}
	array, ok := view.LookupArray(row)
	if !ok || !array.Concrete() || !array.Usage().Valid() {
		t.Fatal("missing executable reciprocal array")
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	usage, err := EngineResolveType(e, parser.TypeName{Name: "pair", ArrayBounds: []int32{-1}}, op)
	if err != nil || usage.Type != array || !usage.Valid() {
		t.Fatal("real column Usage rejected concrete composite array", usage, err)
	}
	domain, err := sqltype.Domain(60000, "wrapped", "public.wrapped", row.Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainArray, err := sqltype.Array(60001, "_wrapped", domain)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := sqltype.Domain(60002, "whole", "public.whole", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := sqltype.Composite(60003, "nested", "public.nested", 60004, []sqltype.CompositeMember{
		{Attribute: 1, Name: "r", Usage: domain.Usage()},
		{Attribute: 2, Name: "a", Usage: array.Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{row, domain, domainArray, whole, nested} {
		if !typedef.StoredArrayElement(typ) {
			t.Fatalf("executable array element %s rejected", typ.Name())
		}
	}
	lookup := sqltype.LookupOnly(60005, "catalog_row", 60006)
	lookupArray := sqltype.LookupArray(60006, "_catalog_row", lookup)
	lookupDomain := sqltype.LookupDomain(60007, "lookup_domain", "public.lookup_domain", row.Usage())
	for _, typ := range []*sqltype.Descriptor{nil, lookup, lookupArray, lookupDomain, testUsage(e, sqltype.Inet).Type, testUsage(e, sqltype.Xid).Type} {
		if typedef.StoredArrayElement(typ) {
			t.Fatal("unsupported array element admitted", typ)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Regclass, sqltype.Char} {
		if !typedef.StoredArrayElement(testUsage(e, id).Type) {
			t.Fatal("incumbent stored scalar array rejected", id)
		}
	}
}
