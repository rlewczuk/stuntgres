package engine

import (
	"context"
	"sort"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Multiple defaults in one namespace are legal after SET SCHEMA, but cannot be
// directly CREATED there. Only additional defaults use a collision-free helper
// schema, which is created before owner groups and removed by the same replay.
func conversionDumpStatements(ctx context.Context, view catalog.Read, roles authorization.Read, definitions []*catalog.Conversion) (statements []dumpDomain, schemas []string, err error) {
	if len(definitions) == 0 {
		return
	}
	sort.Slice(definitions, func(i, j int) bool {
		return conversionSQLName(view, definitions[i]) < conversionSQLName(view, definitions[j])
	})
	type pair struct {
		namespace           ids.ObjectAddress
		source, destination int
	}
	occupiedPairs := make(map[pair]bool)
	for _, descriptor := range view.Descriptors() {
		if d, ok := descriptor.(*catalog.Conversion); ok && d.Seeded() && d.DefaultConversion {
			occupiedPairs[pair{d.Namespace(), d.Source, d.Destination}] = true
		}
	}
	occupiedSchemas := make(map[string]bool)
	for key := range view.Names() {
		if key.ClassID == ids.NamespaceClassID {
			occupiedSchemas[key.Name] = true
		}
	}
	for i, d := range definitions {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err = (catalog.ConversionProvider{}).Validate(view, d); err != nil {
			return nil, nil, err
		}
		owner, ownerErr := roles.Descriptor(d.Owner)
		if ownerErr != nil {
			return nil, nil, ownerErr
		}
		name := conversionSQLName(view, d)
		key := pair{d.Namespace(), d.Source, d.Destination}
		helper := ""
		if d.DefaultConversion && occupiedPairs[key] {
			base := "__stuntgres_conversion_" + strconv.Itoa(i)
			helper = base
			for n := 0; occupiedSchemas[helper]; n++ {
				helper = base + "_" + strconv.Itoa(n)
			}
			occupiedSchemas[helper] = true
			schemas = append(schemas, "CREATE SCHEMA "+catalog.QuoteIdentifier(helper)+" AUTHORIZATION "+catalog.QuoteIdentifier(owner.Name)+";\n")
			name = catalog.QuoteIdentifier(helper) + "." + catalog.QuoteIdentifier(d.Name())
		}
		definition, definitionErr := conversionDefinition(view, d, name)
		if definitionErr != nil {
			return nil, nil, definitionErr
		}
		if helper != "" {
			definition += "ALTER CONVERSION " + name + " SET SCHEMA " + catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + ";\nDROP SCHEMA " + catalog.QuoteIdentifier(helper) + ";\n"
		}
		if d.DefaultConversion {
			occupiedPairs[key] = true
		}
		statements = append(statements, dumpDomain{owner: owner.Name, sql: definition})
	}
	return
}
