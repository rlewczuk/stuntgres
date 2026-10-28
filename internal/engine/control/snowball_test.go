package control

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
	if err := store.InstallSnowballLocked(source.Bootstrap, runtime); err != nil {
		t.Fatal(err)
	}
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
	// All bootstrap dictionaries participate in the same collision graph, including
	// both allocated English and fixed simple, independent of descriptor iteration.
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
	if len(providers) != 2 {
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
}
