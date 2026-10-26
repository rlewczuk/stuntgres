package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func TestRegTSCompoundAdmissionRemap(t *testing.T) {
	ctx := context.Background()
	view := engine.New().CatalogRead()
	alias, _ := view.LookupType(sqltype.Regconfig)
	array, ok := view.LookupArray(alias)
	if !ok || !array.Usage().Valid() || array.Element() != alias {
		t.Fatal("builtin array lacks coherent scalar authority")
	}
	domain, err := sqltype.Domain(940010, "config_domain", "public.config_domain", alias.Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainArray, err := sqltype.Array(940011, "_config_domain", domain)
	if err != nil || domainArray.Element() != domain || !domainArray.Usage().Valid() {
		t.Fatal("domain array admission", err)
	}
	pair, err := sqltype.Composite(940012, "config_pair", "public.config_pair", 0, []sqltype.CompositeMember{
		{Attribute: 1, Name: "v", Usage: alias.Usage()},
		{Attribute: 2, Name: "d", Usage: domain.Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Equal SQL identities from another generation must still be rejected.
	foreign, _ := engine.New().CatalogRead().LookupType(sqltype.Regconfig)
	if _, err := sqltype.Composite(940013, "foreign_pair", "public.foreign_pair", 0, []sqltype.CompositeMember{
		{Attribute: 1, Name: "v", Usage: foreign.Usage()},
		{Attribute: 2, Name: "d", Usage: domain.Usage()},
	}); err == nil {
		t.Fatal("mixed scalar/domain generations admitted")
	}
	oid, _ := view.LookupType(sqltype.Oid)
	class, ok, err := view.DefaultIndexClass(403, oid)
	if err != nil || !ok {
		t.Fatal("actual oid_ops class", err)
	}
	class, err = routine.BindClassSupport(view, class)
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := routine.BindIndexOrdering(view, alias.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	span, _, err := sqltype.NewRangeFamily(940014, "config_span", "public.config_span", 940015, "config_spans", "public.config_spans", sqltype.RangeDefinition{Subtype: alias.Usage(), Class: class, Ordering: ordering}, boolean)
	if err != nil {
		t.Fatal(err)
	}
	row, err := sqltype.Composite(940016, "config_row", "public.config_row", 0, []sqltype.CompositeMember{
		{Attribute: 1, Name: "a", Usage: array.Usage()},
		{Attribute: 2, Name: "d", Usage: domain.Usage()},
		{Attribute: 3, Name: "c", Usage: pair.Usage()},
		{Attribute: 4, Name: "r", Usage: span.Usage()},
	})
	if err != nil {
		t.Fatal("compound member admission", err)
	}
	const sourceOID, targetOID = uint32(3748), uint32(9748)
	leaf, err := sqltype.RegTSValue(alias, sourceOID)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(leaf)
	if err != nil {
		t.Fatal(err)
	}
	elements, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 3}}, []sqltype.Value{leaf, sqltype.Null(alias), leaf})
	if err != nil {
		t.Fatal(err)
	}
	boxed, err := sqltype.CompositeValue(pair, []sqltype.Value{leaf, wrapped})
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := span.Input(ctx, "[3748,3748]", -1)
	if err != nil {
		t.Fatal(err)
	}
	original, err := sqltype.CompositeValue(row, []sqltype.Value{elements, wrapped, boxed, bounds})
	if err != nil {
		t.Fatal(err)
	}
	source := ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(sourceOID)}
	mapped, err := regobj.RemapTextSearchDatum(original, func(address ids.ObjectAddress) ids.ObjectAddress {
		if address != source {
			t.Fatal("unrelated identity reached mapping", address)
		}
		return ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(targetOID)}
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLeaves := func(value sqltype.Value, want uint32) {
		t.Helper()
		arr, box := value.CompositeField(0), value.CompositeField(2)
		leaves := []sqltype.Value{arr.ArrayElement(0), arr.ArrayElement(2), value.CompositeField(1), box.CompositeField(0), box.CompositeField(1)}
		if err := value.CompositeField(3).VisitRangeEndpoints(func(endpoint sqltype.Value) error {
			leaves = append(leaves, endpoint)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(leaves) != 7 || value.Type() != row || !arr.ArrayElement(1).IsNull() || arr.ArrayElement(1).Type() != alias || leaves[2].Type() != domain || leaves[4].Type() != domain {
			t.Fatal("compound shape, NULL or domain identity changed")
		}
		for i, item := range leaves {
			got, err := item.RegTSOID()
			if err != nil || got != want {
				t.Fatal("embedded identity", i, got, want, err)
			}
		}
	}
	assertLeaves(mapped, targetOID)
	assertLeaves(original, sourceOID)
	visited := 0
	if err := regobj.WalkTextSearchReferences(view, original, func(address ids.ObjectAddress) error {
		if address != source {
			t.Fatal("wrong configuration dependency", address)
		}
		visited++
		return nil
	}); err != nil || visited != 7 {
		t.Fatal("actual configuration references", visited, err)
	}
}
