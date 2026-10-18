package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func conversionSQLName(view CatalogRead, d *conversionDescriptor) string {
	return quoteIdentifier(view.Get(d.Namespace()).Name()) + "." + quoteIdentifier(d.Name())
}

func conversionDefinition(view CatalogRead, d *conversionDescriptor, name string) (string, error) {
	procedure, ok := view.Procedure(d.Handler)
	if !ok || procedure.Conversion.Name() == "" {
		return "", fmt.Errorf("stuntgres: unavailable dump conversion provider")
	}
	prefix := "CREATE CONVERSION "
	if d.DefaultConversion {
		prefix = "CREATE DEFAULT CONVERSION "
	}
	routine := quoteIdentifier(view.Get(procedure.Namespace()).Name()) + "." + quoteIdentifier(procedure.Name())
	return prefix + name + " FOR '" + sqltype.EncodingName(d.Source) + "' TO '" + sqltype.EncodingName(d.Destination) + "' FROM " + routine + ";\n", nil
}

// Multiple defaults in one namespace are legal after SET SCHEMA, but cannot be
// directly CREATED there. Only additional defaults use a collision-free helper
// schema, which is created before owner groups and removed by the same replay.
func conversionDumpStatements(ctx context.Context, view CatalogRead, roles authorization.Read, definitions []*conversionDescriptor) (statements []dumpDomain, schemas []string, err error) {
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
		if d, ok := descriptor.(*conversionDescriptor); ok && d.Seeded() && d.DefaultConversion {
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
		if err = (conversionProvider{}).Validate(view, d); err != nil {
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
			schemas = append(schemas, "CREATE SCHEMA "+quoteIdentifier(helper)+" AUTHORIZATION "+quoteIdentifier(owner.Name)+";\n")
			name = quoteIdentifier(helper) + "." + quoteIdentifier(d.Name())
		}
		definition, definitionErr := conversionDefinition(view, d, name)
		if definitionErr != nil {
			return nil, nil, definitionErr
		}
		if helper != "" {
			definition += "ALTER CONVERSION " + name + " SET SCHEMA " + quoteIdentifier(view.Get(d.Namespace()).Name()) + ";\nDROP SCHEMA " + quoteIdentifier(helper) + ";\n"
		}
		if d.DefaultConversion {
			occupiedPairs[key] = true
		}
		statements = append(statements, dumpDomain{owner: owner.Name, sql: definition})
	}
	return
}

// All moving/renamed seeds first receive intermediate names, avoiding swaps and
// collisions with both canonical seed names and current user-object names.
func conversionSeedDumpStatements(ctx context.Context, view CatalogRead, roles authorization.Read) ([]string, error) {
	catalogNamespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if catalogNamespace == nil {
		return nil, fmt.Errorf("stuntgres: missing bootstrap conversion namespace")
	}
	initial := quoteIdentifier(catalogNamespace.Name())
	type delta struct {
		d                       *conversionDescriptor
		fixed, temporary, owner string
	}
	var changes []delta
	var occupied map[string]bool
	for i := range builtinConversions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fixed := &builtinConversions[i]
		d, ok := view.Conversion(fixed.Address())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing pinned conversion")
		}
		if err := (conversionProvider{}).Validate(view, d); err != nil {
			return nil, err
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		change := delta{d: d, fixed: fixed.Name(), owner: owner.Name}
		if d.Name() != fixed.Name() || d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
			if occupied == nil {
				occupied = make(map[string]bool)
				for _, descriptor := range view.Descriptors() {
					if descriptor.Kind() == ids.ConversionObject {
						occupied[descriptor.Name()] = true
					}
				}
				for _, seed := range builtinConversions {
					occupied[seed.Name()] = true
				}
			}
			base := "__stuntgres_conversion_seed_" + strconv.FormatUint(uint64(d.OID), 10)
			change.temporary = base
			for n := 0; occupied[change.temporary]; n++ {
				change.temporary = base + "_" + strconv.Itoa(n)
			}
			occupied[change.temporary] = true
		}
		if change.temporary != "" || change.owner != "postgres" || d.Owner.ID() != authorization.BootstrapPrincipalID {
			changes = append(changes, change)
		}
	}
	var statements []string
	for _, change := range changes {
		if change.temporary != "" {
			statements = append(statements, "ALTER CONVERSION "+initial+"."+quoteIdentifier(change.fixed)+" RENAME TO "+quoteIdentifier(change.temporary)+";\n")
		}
	}
	for _, change := range changes {
		if change.temporary == "" {
			continue
		}
		from := initial + "." + quoteIdentifier(change.temporary)
		if change.d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
			namespace := quoteIdentifier(view.Get(change.d.Namespace()).Name())
			statements = append(statements, "ALTER CONVERSION "+from+" SET SCHEMA "+namespace+";\n")
			from = namespace + "." + quoteIdentifier(change.temporary)
		}
		statements = append(statements, "ALTER CONVERSION "+from+" RENAME TO "+quoteIdentifier(change.d.Name())+";\n")
	}
	for _, change := range changes {
		if change.owner != "postgres" || change.d.Owner.ID() != authorization.BootstrapPrincipalID {
			statements = append(statements, "ALTER CONVERSION "+conversionSQLName(view, change.d)+" OWNER TO "+quoteIdentifier(change.owner)+";\n")
		}
	}
	return statements, nil
}
