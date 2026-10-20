package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Seed identity/provider/pins survive SQL metadata ALTER. Reconstruct metadata
// before user FROM definitions, using collision-free intermediate names for swaps.
func collationSeedDumpStatements(ctx context.Context, view catalog.Read, roles authorization.Read) ([]string, error) {
	catalogNamespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if catalogNamespace == nil {
		return nil, fmt.Errorf("stuntgres: missing bootstrap collation namespace")
	}
	initialSchema := catalog.QuoteIdentifier(catalogNamespace.Name())
	type delta struct {
		d                       *catalog.Collation
		fixed, temporary, owner string
	}
	var changes []delta
	var occupied map[string]bool
	for i := range catalog.BuiltinCollations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fixed := &catalog.BuiltinCollations[i]
		d, ok := view.Collation(fixed.Address())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing pinned collation")
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		change := delta{d: d, fixed: fixed.Name(), owner: owner.Name}
		if d.Name() != fixed.Name() || d.Namespace() != fixed.Namespace() {
			if occupied == nil {
				occupied = make(map[string]bool)
				for _, d := range view.Descriptors() {
					if d.Address().ClassID == ids.CollationClassID {
						occupied[d.Name()] = true
					}
				}
				for i := range catalog.BuiltinCollations {
					occupied[catalog.BuiltinCollations[i].Name()] = true
				}
			}
			base := "__stuntgres_collation_" + strconv.FormatUint(uint64(d.OID), 10)
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
			statements = append(statements, "ALTER COLLATION "+initialSchema+"."+catalog.QuoteIdentifier(change.fixed)+" RENAME TO "+catalog.QuoteIdentifier(change.temporary)+";\n")
		}
	}
	for _, change := range changes {
		if change.temporary == "" {
			continue
		}
		ns := view.Get(change.d.Namespace())
		from := initialSchema + "." + catalog.QuoteIdentifier(change.temporary)
		if change.d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
			statements = append(statements, "ALTER COLLATION "+from+" SET SCHEMA "+catalog.QuoteIdentifier(ns.Name())+";\n")
			from = catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(change.temporary)
		}
		statements = append(statements, "ALTER COLLATION "+from+" RENAME TO "+catalog.QuoteIdentifier(change.d.Name())+";\n")
	}
	for _, change := range changes {
		if change.owner != "postgres" || change.d.Owner.ID() != authorization.BootstrapPrincipalID {
			statements = append(statements, "ALTER COLLATION "+collationSQLName(view, uint32(change.d.OID))+" OWNER TO "+catalog.QuoteIdentifier(change.owner)+";\n")
		}
	}
	return statements, nil
}
