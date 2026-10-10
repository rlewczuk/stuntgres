package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeCatalogSources() error {
	for _, spec := range []struct {
		oid     OID
		name    string
		columns []struct {
			name string
			typ  sqltype.ID
		}
	}{
		{2609, "pg_description", []struct {
			name string
			typ  sqltype.ID
		}{{"objoid", sqltype.Oid}, {"classoid", sqltype.Oid}, {"objsubid", sqltype.Int4}, {"description", sqltype.Text}}},
		{2606, "pg_constraint", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"conname", sqltype.Name}, {"contype", sqltype.Char}, {"conrelid", sqltype.Oid}, {"conbin", sqltype.PgNodeTree}}},
		{2604, "pg_attrdef", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"adrelid", sqltype.Oid}, {"adnum", sqltype.Int2}, {"adbin", sqltype.PgNodeTree}}},
		{1259, "pg_class", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"relname", sqltype.Name}, {"relnamespace", sqltype.Oid}, {"relowner", sqltype.Oid}, {"relkind", sqltype.Char}, {"relnatts", sqltype.Int2}, {"relhasrules", sqltype.Bool}}},
		{2618, "pg_rewrite", []struct {
			name string
			typ  sqltype.ID
		}{{"oid", sqltype.Oid}, {"rulename", sqltype.Name}, {"ev_class", sqltype.Oid}, {"ev_type", sqltype.Char}, {"ev_enabled", sqltype.Char}, {"is_instead", sqltype.Bool}}},
		{12000, "pg_views", []struct {
			name string
			typ  sqltype.ID
		}{{"schemaname", sqltype.Name}, {"viewname", sqltype.Name}, {"viewowner", sqltype.Name}, {"definition", sqltype.Text}}},
		{12001, "pg_prepared_xacts", []struct {
			name string
			typ  sqltype.ID
		}{{"transaction", sqltype.Xid}, {"gid", sqltype.Text}, {"prepared", sqltype.Timestamptz}, {"owner", sqltype.Name}, {"database", sqltype.Name}}},
	} {
		owner := e.roles.names["postgres"]
		d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: spec.oid}, namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, name: spec.name, revision: 1, relationKind: catalogRelation, owner: owner, index: make(map[string]int), attributes: make([]int32, len(spec.columns)), columns: make([]ColumnDescriptor, len(spec.columns)), acl: ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Public: true}, Privileges: SelectPrivilege}}}}
		d.attributeHighWater = int32(len(d.attributes))
		for i, column := range spec.columns {
			typ, ok := (CatalogRead{owner: e, state: e.state}).LookupType(column.typ)
			if !ok {
				return fmt.Errorf("stuntgres: missing catalog source type")
			}
			d.columns[i] = ColumnDescriptor{Name: column.name, Usage: typ.Usage(), Nullable: true, NullabilityKnown: true, OriginTable: uint32(spec.oid), OriginAttribute: int16(i + 1)}
			if spec.oid == 2604 {
				d.columns[i].Nullable = false
			}
			if spec.oid == 2609 {
				d.columns[i].Nullable = false
				if column.typ == sqltype.Text {
					d.columns[i].Usage.Collation.ID = 950
				}
			}
			d.attributes[i] = int32(i + 1)
			d.index[column.name] = i
		}
		e.state.descriptors[d.address] = d
		e.state.names[NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: d.name}] = d.address
	}
	return nil
}

func (e *Engine) captureCatalogSourceLocked(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if source.address.ObjectID == 2609 {
		return capturePGDescription(ctx, catalog, ref, source)
	}
	if source.address.ObjectID == 12001 {
		return e.capturePreparedSourceLocked(ctx, ref, source)
	}
	var relations []*relationDescriptor
	for _, descriptor := range catalog.state.descriptors {
		if relation, ok := descriptor.(*relationDescriptor); ok {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))
	var captureErr error
	integer := func(i int, n int64) sqltype.Value {
		value, err := sqltype.Integer(source.columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value {
		value, err := sqltype.OIDValue(source.columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.columns[i].Usage.Type, b) }
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch source.address.ObjectID {
		case 2606:
			for _, definition := range relation.notNullConstraints {
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "n"), oid(3, uint32(relation.address.ObjectID)), sqltype.Null(source.columns[4].Usage.Type)})
			}
			for _, definition := range relation.checkConstraints {
				tree, err := catalogTree(source.columns[4].Usage.Type, definition.expression)
				if err != nil {
					return nil, err
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "c"), oid(3, uint32(relation.address.ObjectID)), tree})
			}
			for _, definition := range relation.uniqueConstraints {
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), text(1, definition.Name), text(2, "u"), oid(3, uint32(relation.address.ObjectID)), sqltype.Null(source.columns[4].Usage.Type)})
			}
		case 2604:
			for attribute, definition := range relation.defaults {
				tree, err := catalogTree(source.columns[3].Usage.Type, definition.expression)
				if err != nil {
					return nil, err
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), oid(1, uint32(relation.address.ObjectID)), integer(2, int64(attribute)), tree})
			}
			for attribute, definition := range relation.generations {
				tree, err := catalogTree(source.columns[3].Usage.Type, definition.expression)
				if err != nil {
					return nil, err
				}
				rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), oid(1, uint32(relation.address.ObjectID)), integer(2, int64(attribute)), tree})
			}
		case 1259:
			kind := "r"
			if relation.relationKind == sequenceRelation {
				kind = "S"
			} else if relation.relationKind == viewRelation {
				kind = "v"
			} else if relation.relationKind == indexRelation {
				kind = "i"
			}
			rows = append(rows, []sqltype.Value{oid(0, uint32(relation.address.ObjectID)), text(1, relation.name), oid(2, uint32(relation.namespace.ObjectID)), oid(3, uint32(relation.owner.id)), text(4, kind), integer(5, int64(relation.attributeHighWater)), boolean(6, relation.view != nil)})
		case 2618:
			if relation.view == nil {
				continue
			}
			rows = append(rows, []sqltype.Value{oid(0, uint32(relation.view.rule)), text(1, "_RETURN"), oid(2, uint32(relation.address.ObjectID)), text(3, "1"), text(4, "O"), boolean(5, true)})
		case 12000:
			if relation.view == nil {
				continue
			}
			owner, err := (RoleRead{e, e.roles}).Descriptor(relation.owner)
			if err != nil {
				return nil, err
			}
			definition, err := relation.view.selectSQL(ctx, catalog, viewPresentation{columns: relation.columns})
			if err != nil {
				return nil, err
			}
			rows = append(rows, []sqltype.Value{text(0, catalog.state.descriptors[relation.namespace].Name()), text(1, relation.name), text(2, owner.Name), text(3, definition+";")})
		default:
			return nil, fmt.Errorf("stuntgres: unknown catalog source")
		}
		if captureErr != nil {
			return nil, captureErr
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
