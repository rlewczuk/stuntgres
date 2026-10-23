package typedef

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func compositeDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, relation *catalog.Relation) (dump.Statement, error) {
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	if !DefaultDomainACL(row) {
		return dump.Statement{}, fmt.Errorf("stuntgres: dump cannot serialize changed composite ACL")
	}
	owner, err := roles.Descriptor(relation.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	namespace := view.Get(relation.Namespace()).Name()
	var sql strings.Builder
	sql.WriteString("CREATE TYPE " + catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(relation.Name()) + " AS (")
	for i, c := range relation.Columns {
		if err := ctx.Err(); err != nil {
			return dump.Statement{}, err
		}
		if i != 0 {
			sql.WriteString(", ")
		}
		declaration, err := p.ColumnDeclaration(view, c)
		if err != nil {
			return dump.Statement{}, err
		}
		sql.WriteString(catalog.QuoteIdentifier(c.Name) + " " + declaration)
		if c.Usage.Collation != c.Usage.Type.Usage().Collation {
			name := CollationSQLName(view, c.Usage.Collation.ID)
			if name == "" {
				return dump.Statement{}, fmt.Errorf("stuntgres: unsupported composite member collation")
			}
			sql.WriteString(" COLLATE " + name)
		}
	}
	sql.WriteString(");\n")
	return dump.Statement{Owner: owner.Name, SQL: sql.String()}, nil
}

// Render from the captured graph only. All row creators reserve their implicit
// array names here, so domain-on-array replay follows the actual mixed order.
func MixedDumpStatements(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, order []ids.OID, domains map[ids.OID]*catalog.Type, composites map[ids.OID]*catalog.Relation, replay *DomainReplayNames) (map[ids.OID]dump.Statement, error) {
	result := make(map[ids.OID]dump.Statement, len(domains)+len(composites))
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := domains[id]
		if d == nil {
			if relation, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}); ok && relation.RowType != 0 {
				d = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
			}
		}
		if d == nil {
			continue
		}
		if err := replay.Reserve(d); err != nil {
			return nil, err
		}
		var companion *catalog.Type
		if _, custom := d.SQLType.RangeDefinition(); custom {
			companion = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.MultirangeType().ID())})
			if err := replay.Reserve(companion); err != nil {
				return nil, err
			}
		}
		var statement dump.Statement
		var err error
		if composite := composites[id]; composite != nil {
			statement, err = compositeDumpStatement(ctx, p, view, roles, composite)
		} else if domains[id] != nil {
			statement, err = domainDumpStatement(ctx, p, view, roles, d, replay)
		}
		if err != nil {
			return nil, err
		}
		if statement.SQL != "" {
			result[id] = statement
		}
		replay.Associate(d)
		if companion != nil {
			replay.Associate(companion)
		}
	}
	return result, nil
}
