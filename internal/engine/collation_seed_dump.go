package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// Seed identity/provider/pins survive SQL metadata ALTER. Reconstruct metadata
// before user FROM definitions, using collision-free intermediate names for swaps.
func collationSeedDumpStatements(ctx context.Context, view CatalogRead, roles authorization.Read) ([]string, error) {
	catalogNamespace := view.state.descriptors[ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}]
	if catalogNamespace == nil {
		return nil, fmt.Errorf("stuntgres: missing bootstrap collation namespace")
	}
	initialSchema := quoteIdentifier(catalogNamespace.Name())
	type delta struct {
		d                       *collationDescriptor
		fixed, temporary, owner string
	}
	var changes []delta
	var occupied map[string]bool
	for i := range builtinCollations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fixed := &builtinCollations[i]
		d, ok := view.state.descriptors[fixed.Address()].(*collationDescriptor)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing pinned collation")
		}
		owner, err := roles.Descriptor(d.owner)
		if err != nil {
			return nil, err
		}
		change := delta{d: d, fixed: fixed.name, owner: owner.Name}
		if d.name != fixed.name || d.Namespace() != fixed.Namespace() {
			if occupied == nil {
				occupied = make(map[string]bool)
				for _, d := range view.state.descriptors {
					if d.Address().ClassID == CollationClassID {
						occupied[d.Name()] = true
					}
				}
				for i := range builtinCollations {
					occupied[builtinCollations[i].name] = true
				}
			}
			base := "__stuntgres_collation_" + strconv.FormatUint(uint64(d.oid), 10)
			change.temporary = base
			for n := 0; occupied[change.temporary]; n++ {
				change.temporary = base + "_" + strconv.Itoa(n)
			}
			occupied[change.temporary] = true
		}
		if change.temporary != "" || change.owner != "postgres" || d.owner.ID() != authorization.BootstrapPrincipalID {
			changes = append(changes, change)
		}
	}
	var statements []string
	for _, change := range changes {
		if change.temporary != "" {
			statements = append(statements, "ALTER COLLATION "+initialSchema+"."+quoteIdentifier(change.fixed)+" RENAME TO "+quoteIdentifier(change.temporary)+";\n")
		}
	}
	for _, change := range changes {
		if change.temporary == "" {
			continue
		}
		ns := view.state.descriptors[change.d.Namespace()]
		from := initialSchema + "." + quoteIdentifier(change.temporary)
		if change.d.Namespace().ObjectID != PGCatalogNamespaceOID {
			statements = append(statements, "ALTER COLLATION "+from+" SET SCHEMA "+quoteIdentifier(ns.Name())+";\n")
			from = quoteIdentifier(ns.Name()) + "." + quoteIdentifier(change.temporary)
		}
		statements = append(statements, "ALTER COLLATION "+from+" RENAME TO "+quoteIdentifier(change.d.name)+";\n")
	}
	for _, change := range changes {
		if change.owner != "postgres" || change.d.owner.ID() != authorization.BootstrapPrincipalID {
			statements = append(statements, "ALTER COLLATION "+collationSQLName(view, uint32(change.d.oid))+" OWNER TO "+quoteIdentifier(change.owner)+";\n")
		}
	}
	return statements, nil
}

func (e *Engine) validateTemporaryCollationDump(ctx context.Context, view CatalogRead) error {
	var roots []ids.ObjectAddress
	for a, d := range view.state.descriptors {
		if _, ok := d.(*collationDescriptor); !ok {
			continue
		}
		if ns, ok := view.state.descriptors[d.Namespace()].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			roots = append(roots, a)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	edges, err := domainDependencyClosure(ctx, stage, roots)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		d := view.state.descriptors[ids.ObjectAddress{ClassID: edge.address.ClassID, ObjectID: edge.address.ObjectID}]
		if edge.relation != nil {
			d = edge.relation
		}
		if d == nil {
			return fmt.Errorf("stuntgres: missing temporary collation dependent")
		}
		ns, ok := view.state.descriptors[d.Namespace()].(*namespaceDescriptor)
		if !ok || ns.temporary == (SessionToken{}) {
			return fmt.Errorf("stuntgres: dump cannot serialize persistent temporary collation dependency")
		}
	}
	return nil
}
