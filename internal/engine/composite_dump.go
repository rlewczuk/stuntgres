package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func dumpColumnPredecessors(view CatalogRead, relation *relationDescriptor, predecessors []ids.OID) ([]ids.OID, error) {
	if relation.ofType != (ids.ObjectAddress{}) {
		id, err := dumpPredecessor(view, relation.ofType, relation.address)
		if err != nil {
			return nil, err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
	}
	for _, column := range relation.columns {
		id, err := dumpPredecessor(view, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())}, relation.address)
		if err != nil {
			return nil, err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
	}
	return predecessors, nil
}

func compositeDumpStatement(ctx context.Context, view CatalogRead, roles authorization.Read, relation *relationDescriptor) (dumpDomain, error) {
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.rowType)})
	if !defaultDomainACL(row) {
		return dumpDomain{}, fmt.Errorf("stuntgres: dump cannot serialize changed composite ACL")
	}
	owner, err := roles.Descriptor(relation.owner)
	if err != nil {
		return dumpDomain{}, err
	}
	namespace := view.Get(relation.namespace).Name()
	var sql strings.Builder
	sql.WriteString("CREATE TYPE " + quoteIdentifier(namespace) + "." + quoteIdentifier(relation.name) + " AS (")
	for i, c := range relation.columns {
		if err := ctx.Err(); err != nil {
			return dumpDomain{}, err
		}
		if i != 0 {
			sql.WriteString(", ")
		}
		declaration, err := catalogColumnDeclaration(view, c)
		if err != nil {
			return dumpDomain{}, err
		}
		sql.WriteString(quoteIdentifier(c.Name) + " " + declaration)
		if c.Usage.Collation != c.Usage.Type.Usage().Collation {
			name := collationSQLName(view, c.Usage.Collation.ID)
			if name == "" {
				return dumpDomain{}, fmt.Errorf("stuntgres: unsupported composite member collation")
			}
			sql.WriteString(" COLLATE " + name)
		}
	}
	sql.WriteString(");\n")
	return dumpDomain{owner.Name, sql.String()}, nil
}

// Render from the captured graph only. All row creators reserve their implicit
// array names here, so domain-on-array replay follows the actual mixed order.
func mixedDumpStatements(ctx context.Context, view CatalogRead, roles authorization.Read, order []ids.OID, domains map[ids.OID]*typeDescriptor, composites map[ids.OID]*relationDescriptor) (map[ids.OID]dumpDomain, error) {
	replay := newDomainReplayNames()
	result := make(map[ids.OID]dumpDomain, len(domains)+len(composites))
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := domains[id]
		if d == nil {
			if relation, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}); ok && relation.rowType != 0 {
				d = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.rowType)})
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
