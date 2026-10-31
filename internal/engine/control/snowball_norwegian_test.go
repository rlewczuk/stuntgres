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

func TestSnowballNorwegianSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var seed *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "norwegian_stem" {
			seed = d
		}
	}
	stock := []catalog.DictionaryOption{{Name: "language", Value: "norwegian", Supplied: true}, {Name: "stopwords", Value: "norwegian", Supplied: true}}
	if seed == nil || !slices.Equal(seed.SeedOptions(), stock) {
		t.Fatal("genuine Norwegian seed with canonical ordered options", seed)
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", stock, ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "norwegian", Supplied: true}}, " (language, stopwords, language = 'norwegian')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "norwegian", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'norwegian', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "norwegian", Supplied: true}, {Name: "language", Value: "NoRwEgIaN", Supplied: true}}, " (language, stopwords, stopwords = 'norwegian', language = 'NoRwEgIaN')"},
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
	changed.ObjectName = "saved_norwegian"
	changed.Options = []catalog.DictionaryOption{{Name: "language", Value: "norwegian", Supplied: true}}
	runtime, err := functions.CompileSnowballDictionary(t.Context(), "norwegian", "")
	if err != nil {
		t.Fatal(err)
	}
	changed.Runtime = runtime
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(changed.Address(), &changed)
	source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil || len(providers) != 16 {
		t.Fatal("actual dictionary replay inventory", providers, err)
	}
	lines, err := planProviderReplay(providers)
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"norwegian_stem\" RENAME TO \"saved_norwegian\";\n" +
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"saved_norwegian\" (language, stopwords, language = 'norwegian');\n"
	if err != nil || strings.Join(lines, "") != want {
		t.Fatal("canonical identity and removed stopwords replay", lines, err)
	}
	providers, err = script.configurationProviderReplays()
	if err != nil || len(providers) != 16 {
		t.Fatal("actual configuration replay inventory", providers, err)
	}
	if lines, err := planProviderReplay(providers); err != nil || len(lines) != 0 {
		t.Fatal("unchanged configurations duplicated", lines, err)
	}
	address := source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: seed.Namespace(), Name: "norwegian"})
	configuration := source.CatalogView.MustTSConfiguration(address)
	moved := *configuration
	moved.ObjectName = "saved_norwegian_config"
	source.State.UnbindName(catalog.DescriptorNameKey(configuration))
	source.State.SetDescriptor(address, &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), address)
	providers, err = script.configurationProviderReplays()
	if err != nil {
		t.Fatal(err)
	}
	lines, err = planProviderReplay(providers)
	if err != nil || strings.Join(lines, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"norwegian\" RENAME TO \"saved_norwegian_config\";\n" {
		t.Fatal("canonical configuration replay", lines, err)
	}
}
