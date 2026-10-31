package control

import (
	"context"
	"fmt"
	"iter"
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

// Fresh Restore starts with all bootstrap seeds. Retire missing identities before
// either configuration or dictionary name moves can reuse their canonical names.
func (s *dumpScript) snowballSeedRetirements(ctx context.Context, defaults iter.Seq[catalog.Descriptor]) ([]string, error) {
	presentDictionaries, presentConfigurations := map[string]bool{}, map[string]bool{}
	for _, descriptor := range s.CatalogView.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.TSDictionary:
			if d.Seeded() {
				presentDictionaries[d.SeedName()] = true
			}
		case *catalog.TSConfiguration:
			if d.Seeded() {
				presentConfigurations[d.SeedName()] = true
			}
		}
	}
	var dictionaries []*catalog.TSDictionary
	var configurations []*catalog.TSConfiguration
	absent := map[ids.ObjectAddress]bool{}
	missingConfiguration := false
	for descriptor := range defaults {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.TSDictionary:
			if !presentDictionaries[d.SeedName()] {
				dictionaries = append(dictionaries, d)
				absent[d.Address()] = true
			}
		case *catalog.TSConfiguration:
			configurations = append(configurations, d)
			missingConfiguration = missingConfiguration || !presentConfigurations[d.SeedName()]
		}
	}
	if len(dictionaries) == 0 && !missingConfiguration {
		return nil, nil
	}
	slices.SortFunc(dictionaries, func(a, b *catalog.TSDictionary) int { return strings.Compare(a.SeedName(), b.SeedName()) })
	slices.SortFunc(configurations, func(a, b *catalog.TSConfiguration) int { return strings.Compare(a.SeedName(), b.SeedName()) })
	namespace := s.CatalogView.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	qualified := func(name string) string {
		return catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(name)
	}
	var statements []string
	for _, configuration := range configurations {
		if !presentConfigurations[configuration.SeedName()] {
			statements = append(statements, "DROP TEXT SEARCH CONFIGURATION "+qualified(configuration.SeedName())+";\n")
		}
	}
	for _, configuration := range configurations {
		if !presentConfigurations[configuration.SeedName()] {
			continue
		}
		var tokenIDs []int32
		for _, mapping := range configuration.Mappings {
			for _, dictionary := range mapping.Dictionaries {
				if absent[dictionary] {
					tokenIDs = append(tokenIDs, mapping.TokenID)
					break
				}
			}
		}
		if len(tokenIDs) == 0 {
			continue
		}
		parser, ok := s.CatalogView.TSParser(configuration.Parser)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing bootstrap text search parser")
		}
		runtime, err := catalog.ResolveParserRuntime(s.CatalogView, parser)
		if err != nil {
			return nil, err
		}
		tokens, err := runtime.TokenTypes(ctx)
		if err != nil {
			return nil, err
		}
		aliases := make([]string, 0, len(tokenIDs))
		for _, tokenID := range tokenIDs {
			if tokenID < 1 || int(tokenID) > len(tokens) {
				return nil, fmt.Errorf("stuntgres: invalid bootstrap text search token")
			}
			aliases = append(aliases, catalog.QuoteIdentifier(tokens[tokenID-1].Alias))
		}
		// Only fresh bootstrap edges to a missing dictionary are cleared.
		// Existing declaration replay restores the survivor's captured bag.
		statements = append(statements, "ALTER TEXT SEARCH CONFIGURATION "+qualified(configuration.SeedName())+" DROP MAPPING IF EXISTS FOR "+strings.Join(aliases, ", ")+";\n")
	}
	for _, dictionary := range dictionaries {
		statements = append(statements, "DROP TEXT SEARCH DICTIONARY "+qualified(dictionary.SeedName())+";\n")
	}
	return statements, ctx.Err()
}
