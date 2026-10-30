package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestSnowballHungarianSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var dictionary *catalog.TSDictionary
	var configuration *catalog.TSConfiguration
	for _, descriptor := range source.State.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.TSDictionary:
			if d.SeedName() == "hungarian_stem" {
				dictionary = d
			}
		case *catalog.TSConfiguration:
			if d.SeedName() == "hungarian" {
				configuration = d
			}
		}
	}
	if dictionary == nil || configuration == nil {
		t.Fatal("missing actual Hungarian seeds")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", dictionary.SeedOptions(), ""},
		{"removed", []catalog.DictionaryOption{{Name: "language", Value: "hungarian", Supplied: true}}, " (language, stopwords, language = 'hungarian')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "hungarian", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, stopwords, language = 'hungarian', stopwords = '')"},
		{"changed_resource", []catalog.DictionaryOption{{Name: "language", Value: "hungarian", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, stopwords, language = 'hungarian', stopwords = 'english')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "hungarian", Supplied: true}, {Name: "language", Value: "HuNgArIaN", Supplied: true}}, " (language, stopwords, stopwords = 'hungarian', language = 'HuNgArIaN')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *dictionary
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			changes := append([]catalog.DictionaryOption{{Name: "language"}, {Name: "stopwords"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(dictionary.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("ordered stock-stoplist replay", got, tc.options)
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
}
