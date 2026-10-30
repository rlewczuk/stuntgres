package control

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Remove defaults before appending the complete current ordered option list.
// Replaying only supplied values would resurrect a removed stock stoplist.
func seedDictionaryOptionDelta(d *catalog.TSDictionary) string {
	defaults := d.SeedOptions()
	if slices.Equal(defaults, d.Options) {
		return ""
	}
	parts := make([]string, 0, len(defaults)+len(d.Options))
	for _, option := range defaults {
		parts = append(parts, catalog.DependencyIdentifier(option.Name))
	}
	for _, option := range d.Options {
		parts = append(parts, catalog.DictionaryOptionsText([]catalog.DictionaryOption{option}))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// Identity replay precedes declaration/mapping replay; use the ordinary
// collision planner so simple and released Snowball seed name cycles remain real SQL.
func (s *dumpScript) configurationProviderReplays() ([]providerReplay, error) {
	var out []providerReplay
	originalNS := s.CatalogView.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	for _, descriptor := range s.CatalogView.Descriptors() {
		d, ok := descriptor.(*catalog.TSConfiguration)
		if !ok || !d.Seeded() {
			continue
		}
		if err := (catalog.TSConfigurationProvider{}).Validate(s.CatalogView, d); err != nil {
			return nil, err
		}
		ns := s.CatalogView.MustNamespace(d.Namespace())
		p := providerReplay{
			family:       configurationProvider,
			original:     providerKey{originalNS.Name(), d.SeedName(), ""},
			current:      providerKey{ns.Name(), d.Name(), ""},
			ownerChanged: d.Owner != s.CatalogView.Owner().Principals.Principal(authorization.BootstrapPrincipalID),
		}
		if p.ownerChanged {
			owner, err := s.DomainRoles.Descriptor(d.Owner)
			if err != nil {
				return nil, err
			}
			p.owner = owner.Name
		}
		out = append(out, p)
	}
	return out, nil
}

// The restore destination starts with the finite initialized inventory. Remove
// absent intrinsic identities before any name collision planner reuses their
// canonical names. Never infer absence from a public label: a different seed or
// an ordinary declaration can currently occupy that spelling.
func (s *dumpScript) snowballDropReplay(ctx context.Context) ([]string, error) {
	var configurations, mappings, dictionaries []string
	var englishMissing, hindiPresent bool
	namespace := s.CatalogView.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}).Name()
	qualified := func(name string) string { return catalog.QuoteIdentifier(namespace)+"."+catalog.QuoteIdentifier(name) }
	for seed := range s.CatalogView.SnowballSeeds() {
		if err := ctx.Err(); err != nil { return nil, err }
		if !seed.Configuration {
			configurations = append(configurations, "DROP TEXT SEARCH CONFIGURATION "+qualified(seed.Language)+" RESTRICT;\n")
		}
		if seed.Language == "hindi" { hindiPresent = seed.Configuration }
		if seed.Dictionary { continue }
		if seed.Language == "english" { englishMissing = true }
		if seed.Configuration {
			// Clear only initialized edges, before dictionary retirement.
			// The ordinary configuration declaration later restores the
			// complete captured mapping, including renamed/replaced targets.
			tokens := "asciiword, word, hword_part, hword_asciipart, asciihword, hword"
			if seed.Language == "hindi" { tokens = "word, hword_part, hword" }
			mappings = append(mappings, "ALTER TEXT SEARCH CONFIGURATION "+qualified(seed.Language)+" DROP MAPPING IF EXISTS FOR "+tokens+";\n")
		}
		dictionaries = append(dictionaries, "DROP TEXT SEARCH DICTIONARY "+qualified(seed.Language+"_stem")+" RESTRICT;\n")
	}
	if englishMissing && hindiPresent {
		mappings = append(mappings, "ALTER TEXT SEARCH CONFIGURATION "+qualified("hindi")+" DROP MAPPING IF EXISTS FOR asciiword, hword_asciipart, asciihword;\n")
	}
	configurations = append(configurations, mappings...)
	return append(configurations, dictionaries...), nil
}
