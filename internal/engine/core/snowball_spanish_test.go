package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestSnowballSpanishProvider(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: "spanish_stem"})
	d := v.MustTSDictionary(a)
	other := engine.New().CatalogRead().MustTSDictionary(a)
	defaults := []catalog.DictionaryOption{{Name: "language", Value: "spanish", Supplied: true}, {Name: "stopwords", Value: "spanish", Supplied: true}}
	if !d.Seeded() || d.SeedName() != "spanish_stem" || d.Owner.ID() != 10 || !slices.Equal(d.Options, defaults) || !slices.Equal(d.SeedOptions(), defaults) || d.Runtime == nil || d.Runtime != other.Runtime {
		t.Fatal("genuine singleton/defaults", d)
	}
	template, ok := v.TSTemplate(d.Template)
	if !ok {
		t.Fatal("missing Snowball template")
	}
	if !template.Snowball() || !template.Seeded() || template.Name() != "snowball" {
		t.Fatal(template)
	}
	if err := catalog.ValidateSnowballTemplate(v, template); err != nil {
		t.Fatal(err)
	}
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(v, d); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "norwegian", "portuguese"} {
		address := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: language + "_stem"})
		if address == a || address.ObjectID == 0 || v.MustTSDictionary(address).Template != d.Template {
			t.Fatal("distinct shared-template seed", language)
		}
	}
	renamed := *d
	renamed.ObjectName = "english_stem"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if renamed.SeedName() != "spanish_stem" {
		t.Fatal("mutable label granted authority")
	}
	counterfeit := &catalog.TSDictionary{ObjectAddress: a, ObjectNamespace: ns, ObjectName: d.Name(), ObjectRevision: d.Revision(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime}
	if counterfeit.Seeded() || counterfeit.SeedName() != "" || len(counterfeit.SeedOptions()) != 0 {
		t.Fatal("public fields forged seed")
	}
	for _, mutate := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil },
		func(d *catalog.TSDictionary) { d.Options = nil },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}
		},
		func(d *catalog.TSDictionary) {
			d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}
		},
	} {
		bad := *d
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(a, &bad)
		if err := provider.Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("counterfeit runtime admitted", bad)
		}
	}
	for _, tc := range []struct {
		input, want string
		count       int
	}{{"CASAS", "cas", 1}, {"niños", "niñ", 1}, {"más", "", 0}, {"educacion", "educacion", 1}} {
		got, err := d.Runtime.Lexize(t.Context(), tc.input)
		if err != nil || !got.Recognized || len(got.Lexemes) != tc.count || tc.count == 1 && got.Lexemes[0].Text != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestSnowballSpanishPreparedOwnership(t *testing.T) {
	// The composed helper retains real accepted artifacts, their generation,
	// immutable backing and operand provenance across stoplist replacement,
	// fresh binding, live parameter execution and rejected fold publication.
	snowballPreparedOwnership(t, "spanish", "más", "mas")
}

func TestSnowballSpanishPublication(t *testing.T) {
	// Exercise actual compile/stage/complete rejection, not a fake runtime.
	snowballPublication(t, "spanish", "CASAS", "cas")
}
