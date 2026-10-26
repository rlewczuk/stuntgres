package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestDictionaryProviderReplayCycle(t *testing.T) {
	seed := providerReplay{dictionary: true, original: providerKey{"pg_catalog", "simple", ""}, current: providerKey{"pg_catalog", "unaccent", ""}, options: " (stopwords = 'english')"}
	member := providerReplay{dictionary: true, original: providerKey{"pg_catalog", "unaccent", ""}, current: providerKey{"pg_catalog", "simple", ""}, options: " (rules = 'unaccent')", ownerChanged: true, owner: "recipient"}
	reserved := providerReplay{dictionary: true, original: providerKey{"pg_catalog", "__stuntgres_dictionary_1", ""}, current: providerKey{"pg_catalog", "__stuntgres_dictionary_1", ""}}
	want := []string{
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"simple\" RENAME TO \"__stuntgres_dictionary_2\";\n",
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"unaccent\" RENAME TO \"simple\";\n",
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"__stuntgres_dictionary_2\" RENAME TO \"unaccent\";\n",
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"unaccent\" (stopwords = 'english');\n",
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"simple\" (rules = 'unaccent');\n",
		"ALTER TEXT SEARCH DICTIONARY \"pg_catalog\".\"simple\" OWNER TO \"recipient\";\n",
	}
	for _, providers := range [][]providerReplay{{seed, member, reserved}, {reserved, member, seed}} {
		before := slices.Clone(providers)
		got, err := planProviderReplay(providers)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("dictionary cycle/options/owner replay: got %q, want %q: %v", got, want, err)
		}
		if !slices.Equal(providers, before) {
			t.Fatal("planner mutated caller collision keys")
		}
	}
}

func TestDictionaryProviderReplayRelocatedNamespace(t *testing.T) {
	for _, moved := range []bool{false, true} {
		name := "namespace_only"
		if moved {
			name = "dictionary_moved"
		}
		t.Run(name, func(t *testing.T) {
			source, _, _, _ := fdwUnitSource()
			namespace := source.CatalogView.MustNamespace(fdwUnitAddress(ids.NamespaceClassID, ids.PGCatalogNamespaceOID))
			relocated := *namespace
			relocated.ObjectName = "moved_catalog"
			source.State.UnbindName(catalog.DescriptorNameKey(namespace))
			source.State.SetDescriptor(relocated.Address(), &relocated)
			source.State.BindName(catalog.DescriptorNameKey(&relocated), relocated.Address())

			var want []string
			if moved {
				seed, ok := source.CatalogView.TSDictionary(fdwUnitAddress(ids.TSDictionaryClassID, 3765))
				if !ok {
					t.Fatal("missing initialized dictionary")
				}
				dictionary := *seed
				dictionary.ObjectName = "renamed_simple"
				dictionary.ObjectNamespace = fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID)
				recipient, ok := source.DomainRoles.Lookup("pg_monitor")
				if !ok {
					t.Fatal("missing recipient")
				}
				dictionary.Owner = recipient.Ref
				dictionary.Options = []catalog.DictionaryOption{{Name: "accept", Value: "false", Supplied: true}}
				runtime, err := functions.CompileSimpleDictionary(t.Context(), false, "")
				if err != nil {
					t.Fatal(err)
				}
				dictionary.Runtime = runtime
				source.State.UnbindName(catalog.DescriptorNameKey(seed))
				source.State.SetDescriptor(dictionary.Address(), &dictionary)
				source.State.BindName(catalog.DescriptorNameKey(&dictionary), dictionary.Address())
				want = []string{
					"ALTER TEXT SEARCH DICTIONARY \"moved_catalog\".\"simple\" RENAME TO \"renamed_simple\";\n",
					"ALTER TEXT SEARCH DICTIONARY \"moved_catalog\".\"renamed_simple\" SET SCHEMA \"public\";\n",
					"ALTER TEXT SEARCH DICTIONARY \"public\".\"renamed_simple\" (accept = 'false');\n",
					"ALTER TEXT SEARCH DICTIONARY \"public\".\"renamed_simple\" OWNER TO \"pg_monitor\";\n",
				}
			}
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
			providers, err := script.dictionaryProviderReplays()
			if err != nil {
				t.Fatal(err)
			}
			if len(providers) != 1 || providers[0].original != (providerKey{"moved_catalog", "simple", ""}) {
				t.Fatalf("initialized identity after namespace replay: %+v", providers)
			}
			got, err := planProviderReplay(providers)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("relocated namespace replay: got %q, want %q: %v", got, want, err)
			}
		})
	}
}
