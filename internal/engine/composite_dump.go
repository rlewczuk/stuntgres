package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

func dumpColumnPredecessors(view CatalogRead, relation *relationDescriptor, predecessors []OID) ([]OID, error) {
	for _, column := range relation.columns {
		id, err := dumpPredecessor(view, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(column.Usage.Type.ID())}, relation.address)
		if err != nil {
			return nil, err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
	}
	return predecessors, nil
}

func compositeDumpStatement(ctx context.Context, view CatalogRead, roles RoleRead, relation *relationDescriptor) (dumpDomain, error) {
	row := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
	if !defaultDomainACL(row) {
		return dumpDomain{}, fmt.Errorf("stuntgres: dump cannot serialize changed composite ACL")
	}
	owner, err := roles.Descriptor(relation.owner)
	if err != nil {
		return dumpDomain{}, err
	}
	namespace := view.state.descriptors[relation.namespace].Name()
	var sql strings.Builder
	sql.WriteString("CREATE TYPE " + quoteIdentifier(namespace) + "." + quoteIdentifier(relation.name) + " AS (")
	for i, c := range relation.columns {
		if err := ctx.Err(); err != nil {
			return dumpDomain{}, err
		}
		if i != 0 {
			sql.WriteString(", ")
		}
		declaration, err := catalogTypeDeclaration(view, c.Usage)
		if err != nil {
			return dumpDomain{}, err
		}
		sql.WriteString(quoteIdentifier(c.Name) + " " + declaration)
		if c.Usage.Collation != c.Usage.Type.Usage().Collation {
			name := builtinCollationName(c.Usage.Collation.ID)
			if name == "" {
				return dumpDomain{}, fmt.Errorf("stuntgres: unsupported composite member collation")
			}
			sql.WriteString(" COLLATE " + quoteIdentifier(view.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}].Name()) + "." + quoteIdentifier(name))
		}
	}
	sql.WriteString(");\n")
	return dumpDomain{owner.Name, sql.String()}, nil
}

// Render from the captured graph only. All row creators reserve their implicit
// array names here, so domain-on-array replay follows the actual mixed order.
func mixedDumpStatements(ctx context.Context, view CatalogRead, roles RoleRead, order []OID, domains map[OID]*typeDescriptor, composites map[OID]*relationDescriptor) (map[OID]dumpDomain, error) {
	replay := newDomainReplayNames()
	result := make(map[OID]dumpDomain, len(domains)+len(composites))
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := domains[id]
		if d == nil {
			if relation, ok := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: id}].(*relationDescriptor); ok && relation.rowType != 0 {
				d = view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
			}
		}
		if d == nil {
			continue
		}
		if err := replay.reserve(d); err != nil {
			return nil, err
		}
		var statement dumpDomain
		var err error
		if composite := composites[id]; composite != nil {
			statement, err = compositeDumpStatement(ctx, view, roles, composite)
		} else if domains[id] != nil {
			statement, err = domainDumpStatement(ctx, view, roles, d, replay)
		}
		if err != nil {
			return nil, err
		}
		if statement.sql != "" {
			result[id] = statement
		}
		replay.associate(d)
	}
	return result, nil
}
