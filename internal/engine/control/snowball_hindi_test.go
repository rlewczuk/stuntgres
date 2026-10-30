package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func hindiSeedReplay(t *testing.T) {
	source := snowballUnitSource(t)
	var dictionary *catalog.TSDictionary
	var configuration *catalog.TSConfiguration
	var english ids.ObjectAddress
	for _, descriptor := range source.State.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.TSDictionary:
			if d.SeedName() == "hindi_stem" {
				dictionary = d
			}
			if d.SeedName() == "english_stem" {
				english = d.Address()
			}
		case *catalog.TSConfiguration:
			if d.SeedName() == "hindi" {
				configuration = d
			}
		}
	}
	if dictionary == nil || configuration == nil || english.ObjectID == 0 || english == dictionary.Address() {
		t.Fatal("missing actual Hindi/English seeds")
	}
	for _, tc := range []struct {
		name    string
		options []catalog.DictionaryOption
		want    string
	}{
		{"stock", []catalog.DictionaryOption{{Name: "language", Value: "hindi", Supplied: true}}, ""},
		{"added_list", []catalog.DictionaryOption{{Name: "language", Value: "hindi", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}, " (language, language = 'hindi', stopwords = 'english')"},
		{"explicit_empty", []catalog.DictionaryOption{{Name: "language", Value: "hindi", Supplied: true}, {Name: "stopwords", Supplied: true}}, " (language, language = 'hindi', stopwords = '')"},
		{"spelling_order", []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "language", Value: "HiNdI", Supplied: true}}, " (language, stopwords = 'english', language = 'HiNdI')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := *dictionary
			d.Options = tc.options
			if got := seedDictionaryOptionDelta(&d); got != tc.want {
				t.Fatal(got, tc.want)
			}
			changes := append([]catalog.DictionaryOption{{Name: "language"}}, tc.options...)
			if got := extension.MergeDictionaryOptions(dictionary.SeedOptions(), changes); !slices.Equal(got, tc.options) {
				t.Fatal("Hindi language-only ordered replay", got, tc.options)
			}
		})
	}
	seen := map[int32]bool{}
	for _, m := range configuration.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID {
		case 1, 11, 16:
			want = english
		case 2, 10, 17:
			want = dictionary.Address()
		}
		if seen[m.TokenID] || len(m.Dictionaries) != 1 || m.Dictionaries[0] != want {
			t.Fatal("split seed edge", m, want)
		}
		seen[m.TokenID] = true
	}
	for _, token := range []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		if !seen[token] {
			t.Fatal("missing split mapping", token)
		}
	}
	if len(seen) != 19 {
		t.Fatal("unexpected split mappings", seen)
	}
}
