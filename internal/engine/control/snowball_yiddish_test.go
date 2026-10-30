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

func TestSnowballYiddishSeedReplay(t *testing.T) {
	t.Run("absent-intrinsic-identities", yiddishAbsentSeedReplay)
	source := snowballUnitSource(t)
	var seed *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() { if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "yiddish_stem" { seed = d } }
	stock := []catalog.DictionaryOption{{Name: "language", Value: "yiddish", Supplied: true}}
	if seed == nil || !slices.Equal(seed.SeedOptions(), stock) || !slices.Equal(seed.Options, stock) || seed.Address().ClassID != ids.TSDictionaryClassID || seed.Address().ObjectID == 0 { t.Fatal("genuine language-only seed", seed) }
	for _, tc := range []struct { name string; options []catalog.DictionaryOption; want string }{
		{"stock", stock, ""},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "yiddish", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'yiddish', stopwords = '')"},
		{"english", []catalog.DictionaryOption{{Name: "language", Value: "yiddish", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'yiddish', stopwords = 'english')"},
		{"ordered", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "YiDdIsH", Supplied: true}}, " (language, stopwords = 'english', language = 'YiDdIsH')"},
		{"provider", []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}, " (language, language = 'english')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *seed; d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want { t.Fatal("exact ordered delta", got, tc.want) }
			if tc.want != "" { changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...); if got := extension.MergeDictionaryOptions(stock, changes); !slices.Equal(got, tc.options) { t.Fatal("remove/append replay", got, tc.options) } }
		})
	}
	changed := *seed
	changed.ObjectName = "saved_yiddish"
	changed.Options = []catalog.DictionaryOption{{Name: "language", Value: "YiDdIsH", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}
	runtime, err := functions.CompileSnowballDictionary(t.Context(), "YiDdIsH", "english")
	if err != nil { t.Fatal(err) }; changed.Runtime = runtime
	if got, err := runtime.Lexize(t.Context(), "קינדער"); err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "קינד" { t.Fatal(got, err) }
	if got, err := runtime.Lexize(t.Context(), "ONLY"); err != nil || !got.Recognized || len(got.Lexemes) != 0 { t.Fatal(got, err) }
	source.State.UnbindName(catalog.DescriptorNameKey(seed)); source.State.SetDescriptor(changed.Address(), &changed); source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil || len(providers) != 19 { t.Fatal("actual dictionary inventory", providers, err) }
	lines, err := planProviderReplay(providers)
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"yiddish_stem\" RENAME TO \"saved_yiddish\";\n" + "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"saved_yiddish\" (language, language = 'YiDdIsH', stopwords = 'english');\n"
	if err != nil || strings.Join(lines, "") != want || changed.SeedName() != "yiddish_stem" { t.Fatal("canonical creation identity replay", lines, err) }
	a := source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: seed.Namespace(), Name: "yiddish"})
	configuration := source.CatalogView.MustTSConfiguration(a)
	if configuration.SeedName() != "yiddish" || len(configuration.Mappings) != 19 { t.Fatal(configuration) }
	moved := *configuration; moved.ObjectName = "saved_yiddish_config"
	source.State.UnbindName(catalog.DescriptorNameKey(configuration)); source.State.SetDescriptor(a, &moved); source.State.BindName(catalog.DescriptorNameKey(&moved), a)
	providers, err = script.configurationProviderReplays()
	if err != nil || len(providers) != 19 { t.Fatal("actual configuration inventory", providers, err) }
	lines, err = planProviderReplay(providers)
	if err != nil || strings.Join(lines, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"yiddish\" RENAME TO \"saved_yiddish_config\";\n" { t.Fatal("canonical configuration replay", lines, err) }
}
