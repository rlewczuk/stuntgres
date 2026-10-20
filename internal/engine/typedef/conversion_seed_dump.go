package typedef

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func ConversionSQLName(view catalog.Read, d *catalog.Conversion) string {
	return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
}

func ConversionDefinition(view catalog.Read, d *catalog.Conversion, name string) (string, error) {
	procedure, ok := view.Procedure(d.Handler)
	if !ok || procedure.Conversion.Name() == "" {
		return "", fmt.Errorf("stuntgres: unavailable dump conversion provider")
	}
	prefix := "CREATE CONVERSION "
	if d.DefaultConversion {
		prefix = "CREATE DEFAULT CONVERSION "
	}
	routine := catalog.QuoteIdentifier(view.Get(procedure.Namespace()).Name()) + "." + catalog.QuoteIdentifier(procedure.Name())
	return prefix + name + " FOR '" + sqltype.EncodingName(d.Source) + "' TO '" + sqltype.EncodingName(d.Destination) + "' FROM " + routine + ";\n", nil
}

// All moving/renamed seeds first receive intermediate names, avoiding swaps and
// collisions with both canonical seed names and current user-object names.
func ConversionSeedDumpStatements(ctx context.Context, view catalog.Read, roles authorization.Read) ([]string, error) {
	catalogNamespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if catalogNamespace == nil {
		return nil, fmt.Errorf("stuntgres: missing bootstrap conversion namespace")
	}
	initial := catalog.QuoteIdentifier(catalogNamespace.Name())
	type delta struct {
		d                       *catalog.Conversion
		fixed, temporary, owner string
	}
	var changes []delta
	var occupied map[string]bool
	for i := range catalog.BuiltinConversions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fixed := &catalog.BuiltinConversions[i]
		d, ok := view.Conversion(fixed.Address())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing pinned conversion")
		}
		if err := (catalog.ConversionProvider{}).Validate(view, d); err != nil {
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
				for _, seed := range catalog.BuiltinConversions {
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
			statements = append(statements, "ALTER CONVERSION "+initial+"."+catalog.QuoteIdentifier(change.fixed)+" RENAME TO "+catalog.QuoteIdentifier(change.temporary)+";\n")
		}
	}
	for _, change := range changes {
		if change.temporary == "" {
			continue
		}
		from := initial + "." + catalog.QuoteIdentifier(change.temporary)
		if change.d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
			namespace := catalog.QuoteIdentifier(view.Get(change.d.Namespace()).Name())
			statements = append(statements, "ALTER CONVERSION "+from+" SET SCHEMA "+namespace+";\n")
			from = namespace + "." + catalog.QuoteIdentifier(change.temporary)
		}
		statements = append(statements, "ALTER CONVERSION "+from+" RENAME TO "+catalog.QuoteIdentifier(change.d.Name())+";\n")
	}
	for _, change := range changes {
		if change.owner != "postgres" || change.d.Owner.ID() != authorization.BootstrapPrincipalID {
			statements = append(statements, "ALTER CONVERSION "+ConversionSQLName(view, change.d)+" OWNER TO "+catalog.QuoteIdentifier(change.owner)+";\n")
		}
	}
	return statements, nil
}
