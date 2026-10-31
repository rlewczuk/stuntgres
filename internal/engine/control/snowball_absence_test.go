package control

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func yiddishAbsentSeedReplay(t *testing.T) {
	const dropConfig = "DROP TEXT SEARCH CONFIGURATION \"pg_catalog\".\"yiddish\" RESTRICT;\n"
	const dropDictionary = "DROP TEXT SEARCH DICTIONARY \"pg_catalog\".\"yiddish_stem\" RESTRICT;\n"
	const clearMappings = "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"yiddish\" DROP MAPPING IF EXISTS FOR asciiword, word, hword_part, hword_asciipart, asciihword, hword;\n"
	for _, tc := range []struct { name string; dictionary, configuration, collision bool; want string }{
		{"unchanged", true, true, false, ""},
		{"configuration-only", true, false, false, dropConfig},
		{"dictionary-remapped-survivor", false, true, false, clearMappings+dropDictionary},
		{"both", false, false, false, dropConfig+dropDictionary},
		{"other-seed-reuses-canonical-names", false, false, true, dropConfig+dropDictionary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := snowballUnitSource(t)
			ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
			dict := source.CatalogView.MustTSDictionary(source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: "yiddish_stem"}))
			config := source.CatalogView.MustTSConfiguration(source.CatalogView.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ns, Name: "yiddish"}))
			if !tc.dictionary {
				source.State.UnbindName(catalog.DescriptorNameKey(dict)); source.State.RemoveDescriptor(dict.Address())
				if tc.configuration {
					next := *config; next.Mappings = catalog.CloneTSMappings(config.Mappings); next.ObjectRevision++
					for i := range next.Mappings { next.Mappings[i].Dictionaries = []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}} }
					source.State.SetDescriptor(next.Address(), &next)
				}
			}
			if !tc.configuration { source.State.UnbindName(catalog.DescriptorNameKey(config)); source.State.RemoveDescriptor(config.Address()) }
			if tc.collision {
				for _, item := range []struct { class ids.OID; original, current string }{{ids.TSDictionaryClassID, "english_stem", "yiddish_stem"}, {ids.TSConfigurationClassID, "english", "yiddish"}} {
					a := source.CatalogView.NameAddress(ids.NameKey{ClassID: item.class, Namespace: ns, Name: item.original})
					old := source.CatalogView.Get(a)
					source.State.UnbindName(catalog.DescriptorNameKey(old))
					if item.class == ids.TSDictionaryClassID { next := *old.(*catalog.TSDictionary); next.ObjectName = item.current; source.State.SetDescriptor(a, &next); source.State.BindName(catalog.DescriptorNameKey(&next), a)
					} else { next := *old.(*catalog.TSConfiguration); next.ObjectName = item.current; source.State.SetDescriptor(a, &next); source.State.BindName(catalog.DescriptorNameKey(&next), a) }
				}
			}
			seen := 0
			languages := [...]string{"english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "norwegian", "portuguese", "yiddish", "indonesian", "nepali"}
			for seed := range source.CatalogView.SnowballSeeds() {
				if seen >= len(languages) || seed.Language != languages[seen] { t.Fatal("finite intrinsic inventory order", seen, seed) }
				if seed.Language != "yiddish" && (!seed.Dictionary || !seed.Configuration) { t.Fatal("unrelated seed disappeared", seed) }
				seen++
				if seed.Language == "yiddish" && (seed.Dictionary != tc.dictionary || seed.Configuration != tc.configuration) { t.Fatal("intrinsic presence changed with spelling", seed) }
			}
			if seen != 19 { t.Fatal("finite represented inventory", seen) }
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
			lines, err := script.snowballDropReplay(t.Context())
			if err != nil || strings.Join(lines, "") != tc.want { t.Fatal("exact ordered absence prelude", lines, tc.want, err) }
			if tc.collision {
				configs, err := script.configurationProviderReplays(); if err != nil { t.Fatal(err) }
				moves, err := planProviderReplay(configs)
				if err != nil || strings.Join(moves, "") != "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"english\" RENAME TO \"yiddish\";\n" { t.Fatal("canonical config collision", moves, err) }
				dictionaries, err := script.dictionaryProviderReplays(); if err != nil { t.Fatal(err) }
				moves, err = planProviderReplay(dictionaries)
				if err != nil || strings.Join(moves, "") != "ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"english_stem\" RENAME TO \"yiddish_stem\";\n" { t.Fatal("canonical dictionary collision", moves, err) }
			}
		})
	}
	t.Run("represented-English-Hindi-default-edges", func(t *testing.T) {
		source := snowballUnitSource(t)
		for _, raw := range source.State.Descriptors() {
			if d, ok := raw.(*catalog.TSDictionary); ok && d.SeedName() == "english_stem" { source.State.UnbindName(catalog.DescriptorNameKey(d)); source.State.RemoveDescriptor(d.Address()) }
		}
		script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView}}
		lines, err := script.snowballDropReplay(t.Context())
		want := "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"english\" DROP MAPPING IF EXISTS FOR asciiword, word, hword_part, hword_asciipart, asciihword, hword;\n"+
			"ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"hindi\" DROP MAPPING IF EXISTS FOR asciiword, hword_asciipart, asciihword;\n"+
			"DROP TEXT SEARCH DICTIONARY \"pg_catalog\".\"english_stem\" RESTRICT;\n"
		if err != nil || strings.Join(lines, "") != want { t.Fatal("represented baseline edges", lines, err) }
	})
}
