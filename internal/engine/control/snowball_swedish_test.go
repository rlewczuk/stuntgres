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

func TestSnowballSwedishSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var seed *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "swedish_stem" {
			seed = d
		}
	}
	stock := []catalog.DictionaryOption{{Name: "language", Value: "swedish", Supplied: true}, {Name: "stopwords", Value: "swedish", Supplied: true}}
	if seed == nil || !slices.Equal(seed.SeedOptions(), stock) || !slices.Equal(seed.Options, stock) || seed.Address().ClassID != ids.TSDictionaryClassID || seed.Address().ObjectID == 0 {
		t.Fatal("genuine Swedish seed and ordered defaults", seed)
	}
	{
		script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
		dictionaries, err := script.dictionaryProviderReplays()
		if err != nil || len(dictionaries) != 26 {
			t.Fatal("unchanged dictionary replay inventory", dictionaries, err)
		}
		if lines, err := planProviderReplay(dictionaries); err != nil || len(lines) != 0 {
			t.Fatal("unchanged Swedish seed emitted duplicate declarations", lines, err)
		}
		configurations, err := script.configurationProviderReplays()
		if err != nil || len(configurations) != 26 {
			t.Fatal("unchanged configuration replay inventory", configurations, err)
		}
		if lines, err := planProviderReplay(configurations); err != nil || len(lines) != 0 {
			t.Fatal("unchanged Swedish configuration emitted duplicate declarations", lines, err)
		}
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", stock, ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "swedish", Supplied: true}}, " (language, stopwords, language = 'swedish')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "swedish", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'swedish', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "swedish", Supplied: true}, {Name: "language", Value: "SwEdIsH", Supplied: true}}, " (language, stopwords, stopwords = 'swedish', language = 'SwEdIsH')"},
		{"provider", []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}, " (language, stopwords, language = 'english')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *seed
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal("exact ordered default delta", got, tc.want)
			}
			if tc.want != "" {
				changes := append([]catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}, tc.options...)
				if got := extension.MergeDictionaryOptions(stock, changes); !slices.Equal(got, tc.options) {
					t.Fatal("actual remove/append replay", got, tc.options)
				}
			}
		})
	}
	changed := *seed
	changed.ObjectName = "saved_swedish"
	changed.Options = []catalog.DictionaryOption{{Name: "language", Value: "swedish", Supplied: true}}
	runtime, err := functions.CompileSnowballDictionary(t.Context(), "swedish", "")
	if err != nil {
		t.Fatal(err)
	}
	changed.Runtime = runtime
	if got, err := changed.Runtime.Lexize(t.Context(), "sitta"); err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "sitt" {
		t.Fatal("removed stopwords execute real provider", got, err)
	}
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(changed.Address(), &changed)
	source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil || len(providers) != 26 {
		t.Fatal("actual dictionary replay inventory", providers, err)
	}
	lines, err := planProviderReplay(providers)
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"swedish_stem\" RENAME TO \"saved_swedish\";\n" +
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"saved_swedish\" (language, stopwords, language = 'swedish');\n"
	if err != nil || strings.Join(lines, "") != want || changed.SeedName() != "swedish_stem" {
		t.Fatal("canonical identity and removed stopwords replay", lines, err)
	}
	address := source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: seed.Namespace(), Name: "swedish"})
	configuration := source.CatalogView.MustTSConfiguration(address)
	if configuration.SeedName() != "swedish" || len(configuration.Mappings) != 19 {
		t.Fatal("canonical configuration", configuration)
	}
	for _, mapping := range configuration.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch mapping.TokenID {
		case 1, 2, 10, 11, 16, 17:
			want = seed.Address()
		}
		if !slices.Equal(mapping.Dictionaries, []ids.ObjectAddress{want}) {
			t.Fatal("own dictionary without English fallback", mapping)
		}
	}
	moved := *configuration
	moved.ObjectName = "saved_swedish_config"
	source.State.UnbindName(catalog.DescriptorNameKey(configuration))
	source.State.SetDescriptor(address, &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), address)
	providers, err = script.configurationProviderReplays()
	if err != nil || len(providers) != 26 {
		t.Fatal("configuration replay inventory", providers, err)
	}
	lines, err = planProviderReplay(providers)
	if err != nil || strings.Join(lines, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"swedish\" RENAME TO \"saved_swedish_config\";\n" {
		t.Fatal("canonical configuration replay", lines, err)
	}
}
