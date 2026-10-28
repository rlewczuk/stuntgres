package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestSynonymProvider(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.synonym_provider (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "synonym_provider"})
	dictionary := view.MustTSDictionary(address)
	template, ok := view.TSTemplate(dictionary.Template)
	if !ok || !template.Seeded() || template.Address().ObjectID != 3730 || template.Init.ObjectID != 3728 || template.Lexize.ObjectID != 3729 || dictionary.Seeded() {
		t.Fatal("genuine synonym identities", template, dictionary)
	}
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(view, dictionary); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil },
		func(d *catalog.TSDictionary) { d.Runtime = functions.DefaultSimpleDictionary },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "synonyms", Value: "different", Supplied: true}}
		},
		func(d *catalog.TSDictionary) {
			d.Options = append([]catalog.DictionaryOption{{Name: "casesensitive", Value: "true", Supplied: true}}, d.Options...)
		},
		func(d *catalog.TSDictionary) {
			d.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 123456}
		},
		func(d *catalog.TSDictionary) { d.Template.ObjectID = 3727 },
	} {
		bad := *dictionary
		mutate(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := provider.Validate(view.WithState(state), &bad); err == nil {
			t.Fatal("forged runtime/configuration accepted", bad)
		}
	}
	// An authentic helper descriptor is required, not merely an admitted
	// signature, a matching template name, or a SynonymDictionary type assertion.
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Builtin = nil },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Signature = catalog.UnaccentLexizeSignature },
		func(p *catalog.Procedure) {
			p.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 123456}
		},
	} {
		original, _ := view.Procedure(template.Init)
		helper := *original
		mutate(&helper)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(helper.Address(), &helper)
		forged := view.WithState(state)
		if _, err := extension.CompileDictionary(context.Background(), forged, template, dictionary.Options); err == nil {
			t.Fatal("forged helper admitted", helper)
		}
		if err := provider.Validate(forged, dictionary); err == nil {
			t.Fatal("forged helper validated", helper)
		}
	}
	// Repeated synonyms are intentionally last-wins, unlike simple's options.
	options := []catalog.DictionaryOption{{Name: "synonyms", Value: "synonym_missing", Supplied: true}, {Name: "synonyms", Value: "synonym_sample", Supplied: true}, {Name: "casesensitive", Value: "1", Numeric: true, Supplied: true}}
	runtime, err := extension.CompileDictionary(context.Background(), view, template, options)
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := runtime.(*functions.SynonymDictionary)
	if !ok || !compiled.Configured("synonym_sample", true) {
		t.Fatal("ordered options lost", runtime)
	}
	result, err := compiled.Lexize(context.Background(), "POSTGRES")
	if err != nil || result.Recognized {
		t.Fatal("sensitive compiled runtime", result, err)
	}
	result, err = compiled.Lexize(context.Background(), "indices")
	if err != nil || !result.Recognized || len(result.Lexemes) != 1 || result.Lexemes[0].Text != "index" || !result.Lexemes[0].Prefix || result.Lexemes[0].Variant != 0 {
		t.Fatal("real provider prefix", result, err)
	}
}
