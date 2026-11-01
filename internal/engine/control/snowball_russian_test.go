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

func TestSnowballRussianSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var seed, english *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok {
			switch d.SeedName() {
			case "russian_stem":
				seed = d
			case "english_stem":
				english = d
			}
		}
	}
	stock := []catalog.DictionaryOption{{Name: "language", Value: "russian", Supplied: true}, {Name: "stopwords", Value: "russian", Supplied: true}}
	if seed == nil || english == nil || seed.Address() == english.Address() || !slices.Equal(seed.SeedOptions(), stock) || !slices.Equal(seed.Options, stock) || seed.Address().ClassID != ids.TSDictionaryClassID || seed.Address().ObjectID == 0 {
		t.Fatal("real Russian seed and defaults", seed)
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", stock, ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "russian", Supplied: true}}, " (language, stopwords, language = 'russian')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "russian", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'russian', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "russian", Supplied: true}, {Name: "language", Value: "RuSsIaN", Supplied: true}}, " (language, stopwords, stopwords = 'russian', language = 'RuSsIaN')"},
		{"provider", []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}, " (language, stopwords, language = 'english')"},
		{"replacement", []catalog.DictionaryOption{{Name: "language", Value: "russian", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, stopwords, language = 'russian', stopwords = 'english')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *seed
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal("exact ordered delta", got, tc.want)
			}
			if tc.want != "" {
				changes := append([]catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}, tc.options...)
				if got := extension.MergeDictionaryOptions(stock, changes); !slices.Equal(got, tc.options) {
					t.Fatal("remove/append replay", got, tc.options)
				}
			}
		})
	}
	changed := *seed
	changed.ObjectName = "saved_russian"
	changed.Options = []catalog.DictionaryOption{{Name: "language", Value: "russian", Supplied: true}}
	runtime, err := functions.CompileSnowballDictionary(t.Context(), "russian", "")
	if err != nil {
		t.Fatal(err)
	}
	changed.Runtime = runtime
	if got, err := runtime.Lexize(t.Context(), "все"); err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "все" {
		t.Fatal("removed stoplist real output", got, err)
	}
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(changed.Address(), &changed)
	source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil || len(providers) != 30 {
		t.Fatal("dictionary inventory", providers, err)
	}
	lines, err := planProviderReplay(providers)
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"russian_stem\" RENAME TO \"saved_russian\";\n" +
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"saved_russian\" (language, stopwords, language = 'russian');\n"
	if err != nil || strings.Join(lines, "") != want || changed.SeedName() != "russian_stem" {
		t.Fatal("canonical identity and removed stoplist", lines, err)
	}
	a := source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: seed.Namespace(), Name: "russian"})
	configuration := source.CatalogView.MustTSConfiguration(a)
	if configuration.SeedName() != "russian" || len(configuration.Mappings) != 19 {
		t.Fatal("canonical configuration", configuration)
	}
	for _, mapping := range configuration.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch mapping.TokenID {
		case 1, 11, 16:
			want = english.Address()
		case 2, 10, 17:
			want = seed.Address()
		}
		if !slices.Equal(mapping.Dictionaries, []ids.ObjectAddress{want}) {
			t.Fatal("actual English backup/own/simple edge", mapping, want)
		}
	}
	// The mapping edge survives independent renaming of the English seed.
	renamedEnglish := *english
	renamedEnglish.ObjectName = "saved_english"
	source.State.UnbindName(catalog.DescriptorNameKey(english))
	source.State.SetDescriptor(english.Address(), &renamedEnglish)
	source.State.BindName(catalog.DescriptorNameKey(&renamedEnglish), english.Address())
	if source.CatalogView.MustTSConfiguration(a).Mappings[0].Dictionaries[0] != english.Address() {
		t.Fatal("renaming retargeted actual backup")
	}
	moved := *configuration
	moved.ObjectName = "saved_russian_config"
	source.State.UnbindName(catalog.DescriptorNameKey(configuration))
	source.State.SetDescriptor(a, &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), a)
	providers, err = script.configurationProviderReplays()
	if err != nil || len(providers) != 30 {
		t.Fatal("configuration inventory", providers, err)
	}
	lines, err = planProviderReplay(providers)
	if err != nil || strings.Join(lines, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"russian\" RENAME TO \"saved_russian_config\";\n" {
		t.Fatal("canonical configuration replay", lines, err)
	}
}
