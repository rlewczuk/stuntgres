package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func snowballDanishSeedAuthority(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: "danish_stem"})
	d := v.MustTSDictionary(a)
	want := []catalog.DictionaryOption{{Name: "language", Value: "danish", Supplied: true}, {Name: "stopwords", Value: "danish", Supplied: true}}
	if !d.Seeded() || d.SeedName() != "danish_stem" || a.ObjectID == 0 || a.ObjectID == 3765 || !slices.Equal(d.SeedOptions(), want) || !slices.Equal(d.Options, want) || d.Runtime == nil {
		t.Fatal("Danish canonical identity/options/runtime", d)
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v, d); err != nil {
		t.Fatal(err)
	}
	copy := *d
	copy.ObjectName = "english_stem"
	copy.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	for _, options := range [][]catalog.DictionaryOption{
		{{Name: "language", Value: "danish", Supplied: true}},
		{{Name: "language", Value: "DaNiSh", Supplied: true}, {Name: "stopwords", Value: "danish", Supplied: true}},
		{{Name: "language", Value: "english", Supplied: true}, {Name: "stopwords", Value: "danish", Supplied: true}},
	} {
		bad := *d
		bad.Options = options
		state := catalog.CloneState(v.State())
		state.SetDescriptor(a, &bad)
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("mismatched Danish runtime/options authority admitted", options)
		}
	}
	copy.ObjectAddress.ObjectID += 100
	if copy.SeedName() != "danish_stem" || !slices.Equal(copy.SeedOptions(), want) {
		t.Fatal("mutable labels changed Danish replay authority")
	}
	forged := &catalog.TSDictionary{ObjectAddress: d.Address(), ObjectNamespace: d.Namespace(), ObjectRevision: d.Revision(), ObjectName: d.Name(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime}
	if forged.Seeded() || forged.SeedName() != "" || len(forged.SeedOptions()) != 0 {
		t.Fatal("public fields forged Danish authority")
	}
	c := v.MustTSConfiguration(v.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ns, Name: "danish"}))
	if !c.Seeded() || c.SeedName() != "danish" || c.Parser.ObjectID != 3722 || len(c.Mappings) != 19 {
		t.Fatal("actual Danish configuration", c)
	}
	for _, m := range c.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID {
		case 1, 2, 10, 11, 16, 17:
			want = a
		}
		if len(m.Dictionaries) != 1 || m.Dictionaries[0] != want {
			t.Fatal("Danish mapping authority", m)
		}
	}
	cloned := *c
	cloned.ObjectName = "english"
	cloned.ObjectAddress.ObjectID += 100
	if cloned.SeedName() != "danish" {
		t.Fatal("configuration label erased seed authority")
	}
	fake := &catalog.TSConfiguration{ObjectAddress: c.Address(), ObjectNamespace: c.Namespace(), ObjectRevision: c.Revision(), ObjectName: c.Name(), Owner: c.Owner, Parser: c.Parser, Mappings: c.Mappings}
	if fake.Seeded() || fake.SeedName() != "" {
		t.Fatal("public fields forged configuration authority")
	}
}
