package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func snowballDutchSeedAuthority(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: "dutch_stem"})
	d := v.MustTSDictionary(a)
	want := []catalog.DictionaryOption{{Name: "language", Value: "dutch", Supplied: true}, {Name: "stopwords", Value: "dutch", Supplied: true}}
	if !d.Seeded() || d.SeedName() != "dutch_stem" || a.ObjectID == 0 || a.ObjectID == 3765 || !slices.Equal(d.SeedOptions(), want) || !slices.Equal(d.Options, want) || d.Runtime == nil {
		t.Fatal("Dutch canonical identity/options/runtime", d)
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v, d); err != nil {
		t.Fatal(err)
	}
	copy := *d
	copy.ObjectName = "english_stem"
	copy.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	copy.Owner = e.Roles().LookupRef("pg_monitor")
	copy.ObjectAddress.ObjectID += 100
	if copy.SeedName() != "dutch_stem" || !slices.Equal(copy.SeedOptions(), want) {
		t.Fatal("mutable labels/principal erased Dutch authority")
	}
	for _, options := range [][]catalog.DictionaryOption{
		{{Name: "language", Value: "dutch", Supplied: true}},
		{{Name: "language", Value: "DuTcH", Supplied: true}, {Name: "stopwords", Value: "dutch", Supplied: true}},
		{{Name: "language", Value: "english", Supplied: true}, {Name: "stopwords", Value: "dutch", Supplied: true}},
	} {
		bad := *d
		bad.Options = options
		state := catalog.CloneState(v.State())
		state.SetDescriptor(a, &bad)
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("mismatched Dutch runtime/options authority admitted", options)
		}
	}
	forged := &catalog.TSDictionary{ObjectAddress: d.Address(), ObjectNamespace: d.Namespace(), ObjectRevision: d.Revision(), ObjectName: d.Name(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime}
	if forged.Seeded() || forged.SeedName() != "" || len(forged.SeedOptions()) != 0 {
		t.Fatal("public fields forged Dutch authority")
	}
	c := v.MustTSConfiguration(v.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ns, Name: "dutch"}))
	if !c.Seeded() || c.SeedName() != "dutch" || c.Parser.ObjectID != 3722 || len(c.Mappings) != 19 {
		t.Fatal("actual Dutch configuration", c)
	}
	for _, m := range c.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID {
		case 1, 2, 10, 11, 16, 17:
			want = a
		}
		if len(m.Dictionaries) != 1 || m.Dictionaries[0] != want {
			t.Fatal("Dutch mapping authority", m)
		}
	}
	cloned := *c
	cloned.ObjectName = "english"
	cloned.ObjectAddress.ObjectID += 100
	cloned.Owner = copy.Owner
	if cloned.SeedName() != "dutch" {
		t.Fatal("configuration label erased seed authority")
	}
	fake := &catalog.TSConfiguration{ObjectAddress: c.Address(), ObjectNamespace: c.Namespace(), ObjectRevision: c.Revision(), ObjectName: c.Name(), Owner: c.Owner, Parser: c.Parser, Mappings: c.Mappings}
	if fake.Seeded() || fake.SeedName() != "" {
		t.Fatal("public fields forged configuration authority")
	}
}
