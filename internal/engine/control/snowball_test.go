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

func TestSnowballSeedReplay(t *testing.T) {
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
	if err := store.InstallSnowballLocked(source.Bootstrap, runtime, arabicRuntime); err != nil {
		t.Fatal(err)
	}
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	catalog.InstallBuiltinTSParsers(source.State)
	source.CatalogView.Owner().Providers[ids.TSDictionaryClassID] = extension.CatalogProvider{Class: ids.TSDictionaryClassID}
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
	// All bootstrap dictionaries participate in the same collision graph, including
	// allocated English/Arabic and fixed simple, independent of descriptor iteration.
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
	if len(providers) != 3 {
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
