package control

import (
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func snowballUnitSource(t *testing.T) Source {
	t.Helper()
	source, _, _, _ := fdwUnitSource()
	store := catalog.NewStore(*source.CatalogView.Owner(), 30000, 1)
	store.State = source.State
	runtime, err := functions.DefaultSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	arabicRuntime, err := functions.DefaultArabicSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	armenianRuntime, err := functions.DefaultArmenianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	basqueRuntime, err := functions.DefaultBasqueSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	danishRuntime, err := functions.DefaultDanishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	dutchRuntime, err := functions.DefaultDutchSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	estonianRuntime, err := functions.DefaultEstonianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	germanRuntime, err := functions.DefaultGermanSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	greekRuntime, err := functions.DefaultGreekSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallSnowballLocked(source.Bootstrap, runtime, arabicRuntime, armenianRuntime, basqueRuntime, danishRuntime, dutchRuntime, estonianRuntime, germanRuntime, greekRuntime); err != nil {
		t.Fatal(err)
	}
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	catalog.InstallBuiltinTSParsers(source.State)
	catalog.InstallBuiltinTSConfigurations(source.State, source.Bootstrap)
	source.CatalogView.Owner().Providers[ids.TSDictionaryClassID] = extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	return source
}

func TestSnowballSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var seed *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "english_stem" {
			seed = d
		}
	}
	if seed == nil {
		t.Fatal("missing English seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", seed.SeedOptions(), ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}, " (language, stopwords, language = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'english', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "EnGlIsH", Supplied: true}}, " (language, stopwords, stopwords = 'english', language = 'EnGlIsH')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *seed
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if tc.want != "" {
				changes := []catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}
				changes = append(changes, tc.options...)
				if got := extension.MergeDictionaryOptions(d.SeedOptions(), changes); !slices.Equal(got, tc.options) {
					t.Fatal("ordered default delta", got, tc.options)
				}
			}
		})
	}
	var arabic *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "arabic_stem" {
			arabic = d
		}
	}
	if arabic == nil {
		t.Fatal("missing Arabic seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", []catalog.DictionaryOption{{Name: "language", Value: "arabic", Supplied: true}}, ""},
		{"added_list", []catalog.DictionaryOption{{Name: "language", Value: "arabic", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'arabic', stopwords = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "arabic", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'arabic', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "ArAbIc", Supplied: true}}, " (language, stopwords = 'english', language = 'ArAbIc')"},
	} {
		t.Run("arabic/"+tc.name, func(t *testing.T) {
			d := *arabic
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(arabic.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("Arabic ordered language-only delta", got, tc.options)
			}
		})
	}
	var armenian *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "armenian_stem" {
			armenian = d
		}
	}
	if armenian == nil {
		t.Fatal("missing Armenian seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", []catalog.DictionaryOption{{Name: "language", Value: "armenian", Supplied: true}}, ""},
		{"removed_list", []catalog.DictionaryOption{{Name: "language", Value: "armenian", Supplied: true}}, ""},
		{"added_list", []catalog.DictionaryOption{{Name: "language", Value: "armenian", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'armenian', stopwords = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "armenian", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'armenian', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "ArMeNiAn", Supplied: true}}, " (language, stopwords = 'english', language = 'ArMeNiAn')"},
	} {
		t.Run("armenian/"+tc.name, func(t *testing.T) {
			d := *armenian
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			// Apply the actual emitted remove/append contract after stock
			// initialization, not after the source's edited options.
			changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(armenian.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("Armenian ordered language-only delta", got, tc.options)
			}
			withList := append(armenian.SeedOptions(), catalog.DictionaryOption{Name: "stopwords", Value: "english", Supplied: true})
			if tc.name == "removed_list" {
				removed := extension.MergeDictionaryOptions(withList, []catalog.DictionaryOption{{Name: "stopwords"}})
				if !slices.Equal(removed, tc.options) {
					t.Fatal("Armenian explicit list removal", removed, tc.options)
				}
			}
		})
	}
	var basque *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "basque_stem" {
			basque = d
		}
	}
	if basque == nil {
		t.Fatal("missing Basque seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", []catalog.DictionaryOption{{Name: "language", Value: "basque", Supplied: true}}, ""},
		{"removed_list", []catalog.DictionaryOption{{Name: "language", Value: "basque", Supplied: true}}, ""},
		{"added_list", []catalog.DictionaryOption{{Name: "language", Value: "basque", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'basque', stopwords = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "basque", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'basque', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "BaSqUe", Supplied: true}}, " (language, stopwords = 'english', language = 'BaSqUe')"},
	} {
		t.Run("basque/"+tc.name, func(t *testing.T) {
			d := *basque
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			// Apply the actual emitted remove/append contract after stock
			// initialization, not after the source's edited options.
			changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(basque.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("Basque ordered language-only delta", got, tc.options)
			}
			withList := append(basque.SeedOptions(), catalog.DictionaryOption{Name: "stopwords", Value: "english", Supplied: true})
			if tc.name == "removed_list" {
				removed := extension.MergeDictionaryOptions(withList, []catalog.DictionaryOption{{Name: "stopwords"}})
				if !slices.Equal(removed, tc.options) {
					t.Fatal("Basque explicit list removal", removed, tc.options)
				}
			}
		})
	}
	var danish *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "danish_stem" {
			danish = d
		}
	}
	if danish == nil {
		t.Fatal("missing Danish seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", danish.SeedOptions(), ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "danish", Supplied: true}}, " (language, stopwords, language = 'danish')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "danish", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'danish', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "danish", Supplied: true}, {Name: "language", Value: "DaNiSh", Supplied: true}}, " (language, stopwords, stopwords = 'danish', language = 'DaNiSh')"},
	} {
		t.Run("danish/"+tc.name, func(t *testing.T) {
			d := *danish
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if tc.want != "" {
				changes := append([]catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}, tc.options...)
				if got := extension.MergeDictionaryOptions(d.SeedOptions(), changes); !slices.Equal(got, tc.options) {
					t.Fatal("Danish ordered canonical default delta", got, tc.options)
				}
			}
		})
	}
	var dutch *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "dutch_stem" {
			dutch = d
		}
	}
	if dutch == nil {
		t.Fatal("missing Dutch seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", dutch.SeedOptions(), ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "dutch", Supplied: true}}, " (language, stopwords, language = 'dutch')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "dutch", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'dutch', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "dutch", Supplied: true}, {Name: "language", Value: "DuTcH", Supplied: true}}, " (language, stopwords, stopwords = 'dutch', language = 'DuTcH')"},
	} {
		t.Run("dutch/"+tc.name, func(t *testing.T) {
			d := *dutch
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if tc.want != "" {
				changes := append([]catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}, tc.options...)
				if got := extension.MergeDictionaryOptions(d.SeedOptions(), changes); !slices.Equal(got, tc.options) {
					t.Fatal("Dutch ordered canonical default delta", got, tc.options)
				}
			}
		})
	}
	var estonian *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "estonian_stem" {
			estonian = d
		}
	}
	if estonian == nil {
		t.Fatal("missing Estonian seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", []catalog.DictionaryOption{{Name: "language", Value: "estonian", Supplied: true}}, ""},
		{"removed_list", []catalog.DictionaryOption{{Name: "language", Value: "estonian", Supplied: true}}, ""},
		{"added_list", []catalog.DictionaryOption{{Name: "language", Value: "estonian", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'estonian', stopwords = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "estonian", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'estonian', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "EsToNiAn", Supplied: true}}, " (language, stopwords = 'english', language = 'EsToNiAn')"},
	} {
		t.Run("estonian/"+tc.name, func(t *testing.T) {
			d := *estonian
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			// Apply the actual emitted remove/append contract after stock
			// initialization, not after the source's edited options.
			changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(estonian.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("Estonian ordered language-only delta", got, tc.options)
			}
			withList := append(estonian.SeedOptions(), catalog.DictionaryOption{Name: "stopwords", Value: "english", Supplied: true})
			if tc.name == "removed_list" {
				removed := extension.MergeDictionaryOptions(withList, []catalog.DictionaryOption{{Name: "stopwords"}})
				if !slices.Equal(removed, tc.options) {
					t.Fatal("Estonian explicit list removal", removed, tc.options)
				}
			}
		})
	}
	var german *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "german_stem" {
			german = d
		}
	}
	if german == nil {
		t.Fatal("missing German seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", german.SeedOptions(), ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "german", Supplied: true}}, " (language, stopwords, language = 'german')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "german", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'german', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "german", Supplied: true}, {Name: "language", Value: "GeRmAn", Supplied: true}}, " (language, stopwords, stopwords = 'german', language = 'GeRmAn')"},
	} {
		t.Run("german/"+tc.name, func(t *testing.T) {
			d := *german
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if tc.want != "" {
				changes := append([]catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}, tc.options...)
				if got := extension.MergeDictionaryOptions(d.SeedOptions(), changes); !slices.Equal(got, tc.options) {
					t.Fatal("German ordered canonical default delta", got, tc.options)
				}
			}
		})
	}
	// All bootstrap dictionaries participate in the same collision graph, including
	// allocated English/Arabic/Armenian/Basque/Danish/Dutch/Estonian/German/Greek and fixed simple, independent of descriptor iteration.
	changed := *seed
	changed.ObjectName = "simple"
	changed.Options = []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}
	r, err := functions.CompileSnowballDictionary(t.Context(), "english", "")
	if err != nil {
		t.Fatal(err)
	}
	changed.Runtime = r
	simple := source.State.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	moved := *simple
	moved.ObjectName = "english_stem"
	source.State.UnbindName(catalog.DescriptorNameKey(simple))
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(changed.Address(), &changed)
	source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	source.State.SetDescriptor(moved.Address(), &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 10 {
		t.Fatal("actual initialized dictionary inventory", providers)
	}
	lines, err := planProviderReplay(providers)
	if err != nil {
		t.Fatal(err)
	}
	// Three collision moves and one explicit removal/options refresh; no CREATE.
	if len(lines) != 4 {
		t.Fatal(lines)
	}
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"simple\" (language, stopwords, language = 'english');\n"
	if lines[3] != want {
		t.Fatal(lines, want)
	}
	// Intrinsic helpers and the private-marker template replay their actual
	// bootstrap identities, never ordinary CREATE declarations or release slots.
	template, ok := source.CatalogView.TSTemplate(seed.Template)
	if !ok {
		t.Fatal("missing genuine Snowball template")
	}
	init := source.CatalogView.MustProcedure(template.Init)
	renamedInit := *init
	renamedInit.ObjectName = "saved_snowball_init"
	source.State.UnbindName(catalog.DescriptorNameKey(init))
	source.State.SetDescriptor(init.Address(), &renamedInit)
	source.State.BindName(catalog.DescriptorNameKey(&renamedInit), init.Address())
	renamedTemplate := *template
	renamedTemplate.ObjectName = "saved_snowball_template"
	source.State.UnbindName(catalog.DescriptorNameKey(template))
	source.State.SetDescriptor(template.Address(), &renamedTemplate)
	source.State.BindName(catalog.DescriptorNameKey(&renamedTemplate), template.Address())
	templates, err := script.templateProviderReplays()
	if err != nil {
		t.Fatal(err)
	}
	templateLines, err := planProviderReplay(templates)
	if err != nil || len(templateLines) != 1 || templateLines[0] != "ALTER TEXT SEARCH TEMPLATE \"pg_catalog\".\"snowball\" RENAME TO \"saved_snowball_template\";\n" {
		t.Fatal("canonical intrinsic template replay", templateLines, err)
	}
	collection := &dumpCollection{Capture: script.Capture, source: source}
	if err := collection.collectProvider(&renamedInit); err != nil {
		t.Fatal(err)
	}
	helperLines, err := planProviderReplay(collection.Providers)
	if err != nil || len(helperLines) != 1 || helperLines[0] != "ALTER FUNCTION \"pg_catalog\".\"dsnowball_init\"(pg_catalog.internal) RENAME TO \"saved_snowball_init\";\n" {
		t.Fatal("canonical allocated helper replay", helperLines, err)
	}
	if strings.Contains(strings.Join(append(templateLines, helperLines...), ""), "CREATE") {
		t.Fatal("intrinsic replay duplicated bootstrap identities")
	}
}

func TestSnowballGreekSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var seed *catalog.TSDictionary
	for _, descriptor := range source.State.Descriptors() {
		if d, ok := descriptor.(*catalog.TSDictionary); ok && d.SeedName() == "greek_stem" {
			seed = d
		}
	}
	if seed == nil {
		t.Fatal("missing genuine Greek seed")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", []catalog.DictionaryOption{{Name: "language", Value: "greek", Supplied: true}}, ""},
		{"removed_list", []catalog.DictionaryOption{{Name: "language", Value: "greek", Supplied: true}}, ""},
		{"added_list", []catalog.DictionaryOption{{Name: "language", Value: "greek", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'greek', stopwords = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "greek", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'greek', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "GrEeK", Supplied: true}}, " (language, stopwords = 'english', language = 'GrEeK')"},
		{"provider", []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}, " (language, language = 'english')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *seed
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal("exact ordered language-only delta", got, tc.want)
			}
			if tc.want != "" {
				changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
				if got := extension.MergeDictionaryOptions(seed.SeedOptions(), changes); !slices.Equal(got, tc.options) {
					t.Fatal("Greek ordered canonical replay", got, tc.options)
				}
			}
			if tc.name == "removed_list" {
				withList := append(seed.SeedOptions(), catalog.DictionaryOption{Name: "stopwords", Value: "english", Supplied: true})
				if got := extension.MergeDictionaryOptions(withList, []catalog.DictionaryOption{{Name: "stopwords"}}); !slices.Equal(got, tc.options) {
					t.Fatal("Greek explicit list removal", got, tc.options)
				}
			}
		})
	}
	changed := *seed
	changed.ObjectName = "saved_greek"
	changed.Options = []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "GrEeK", Supplied: true}}
	runtime, err := functions.CompileSnowballDictionary(t.Context(), "GrEeK", "english")
	if err != nil {
		t.Fatal(err)
	}
	changed.Runtime = runtime
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(changed.Address(), &changed)
	source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
	providers, err := script.dictionaryProviderReplays()
	if err != nil || len(providers) != 10 {
		t.Fatal("actual Greek dictionary replay inventory", providers, err)
	}
	lines, err := planProviderReplay(providers)
	want := "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"greek_stem\" RENAME TO \"saved_greek\";\n" +
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"saved_greek\" (language, stopwords = 'english', language = 'GrEeK');\n"
	if err != nil || strings.Join(lines, "") != want {
		t.Fatal("canonical Greek identity and exact options replay", lines, err)
	}
	providers, err = script.configurationProviderReplays()
	if err != nil || len(providers) != 10 {
		t.Fatal("actual Greek configuration replay inventory", providers, err)
	}
	if lines, err := planProviderReplay(providers); err != nil || len(lines) != 0 {
		t.Fatal("unchanged configurations duplicated", lines, err)
	}
	address := source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: seed.Namespace(), Name: "greek"})
	configuration := source.CatalogView.MustTSConfiguration(address)
	moved := *configuration
	moved.ObjectName = "saved_greek_config"
	source.State.UnbindName(catalog.DescriptorNameKey(configuration))
	source.State.SetDescriptor(address, &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), address)
	providers, err = script.configurationProviderReplays()
	if err != nil {
		t.Fatal(err)
	}
	lines, err = planProviderReplay(providers)
	if err != nil || strings.Join(lines, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"greek\" RENAME TO \"saved_greek_config\";\n" {
		t.Fatal("canonical Greek configuration replay", lines, err)
	}
}
