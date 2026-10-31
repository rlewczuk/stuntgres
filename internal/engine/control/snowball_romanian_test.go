package control

import (
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestSnowballRomanianSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var dictionary *catalog.TSDictionary
	var configuration *catalog.TSConfiguration
	for _, descriptor := range source.State.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.TSDictionary:
			if d.SeedName() == "romanian_stem" {
				dictionary = d
			}
		case *catalog.TSConfiguration:
			if d.SeedName() == "romanian" {
				configuration = d
			}
		}
	}
	if dictionary == nil || configuration == nil {
		t.Fatal("missing actual Romanian seeds")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", dictionary.SeedOptions(), ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "romanian", Supplied: true}}, ""},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "romanian", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'romanian', stopwords = '')"},
		{"added_resource", []catalog.DictionaryOption{{Name: "language", Value: "romanian", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'romanian', stopwords = 'english')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "RoMaNiAn", Supplied: true}}, " (language, stopwords = 'english', language = 'RoMaNiAn')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *dictionary
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(dictionary.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("ordered language-only replay", got, tc.options)
			}
		})
	}
	seen := map[int32]bool{}
	for _, m := range configuration.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID {
		case 1, 2, 10, 11, 16, 17:
			want = dictionary.Address()
		}
		if seen[m.TokenID] || len(m.Dictionaries) != 1 || m.Dictionaries[0] != want {
			t.Fatal("actual seed edge", m, want)
		}
		seen[m.TokenID] = true
	}
	for _, token := range []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		if !seen[token] {
			t.Fatal("missing mapping", token)
		}
	}
	if len(seen) != 19 {
		t.Fatal("unexpected mappings", seen)
	}

	changed := *dictionary
	changed.ObjectName = "saved_romanian"
	changed.Options = []catalog.DictionaryOption{{Name: "language", Value: "romanian", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}
	runtime, err := functions.CompileSnowballDictionary(t.Context(), "romanian", "english")
	if err != nil {
		t.Fatal(err)
	}
	changed.Runtime = runtime
	if got, err := runtime.Lexize(t.Context(), "THE"); err != nil || !got.Recognized || len(got.Lexemes) != 0 {
		t.Fatal(got, err)
	}
	if got, err := runtime.Lexize(t.Context(), "OCOLUL"); err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "ocol" {
		t.Fatal(got, err)
	}
	source.State.UnbindName(catalog.DescriptorNameKey(dictionary))
	source.State.SetDescriptor(changed.Address(), &changed)
	source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil || len(providers) != 21 {
		t.Fatal("actual dictionary replay inventory", providers, err)
	}
	lines, err := planProviderReplay(providers)
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"romanian_stem\" RENAME TO \"saved_romanian\";\n" +
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"saved_romanian\" (language, language = 'romanian', stopwords = 'english');\n"
	if err != nil || strings.Join(lines, "") != want || changed.SeedName() != "romanian_stem" {
		t.Fatal("canonical dictionary replay", lines, err)
	}
	moved := *configuration
	moved.ObjectName = "saved_romanian_config"
	source.State.UnbindName(catalog.DescriptorNameKey(configuration))
	source.State.SetDescriptor(moved.Address(), &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	providers, err = script.configurationProviderReplays()
	if err != nil || len(providers) != 21 {
		t.Fatal("actual configuration replay inventory", providers, err)
	}
	lines, err = planProviderReplay(providers)
	if err != nil || strings.Join(lines, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"romanian\" RENAME TO \"saved_romanian_config\";\n" {
		t.Fatal("canonical configuration replay", lines, err)
	}
}
