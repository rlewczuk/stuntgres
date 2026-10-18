package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const pgTypeOID ids.OID = 1247

var pgTypeColumnIDs = [...]sqltype.ID{26, 19, 26, 26, 21, 16, 18, 18, 16, 16, 18, 26, 24, 26, 26, 24, 24, 24, 24, 24, 24, 24, 18, 18, 16, 26, 23, 23, 26, 194, 25, 1034}

// Inventory evidence is separate from REGTYPE lookup and codec admission.
// Every companion must be the captured element's canonical array generation.
func pgTypeAdmitted(catalog CatalogRead, d *typeDescriptor) bool {
	if d == nil || d.typ == nil {
		return false
	}
	if d.typ.IsArray() {
		element, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(d.typ.Element().ID())}].(*typeDescriptor)
		return ok && element.typ == d.typ.Element() && element.array == d.typ.ID() && pgTypeAdmitted(catalog, element)
	}
	if d.owningRelation != (ids.ObjectAddress{}) {
		r, ok := catalog.state.descriptors[d.owningRelation].(*relationDescriptor)
		return ok && r.rowType == d.typ.ID()
	}
	_, ok := d.typ.PGTypeMetadata()
	return ok
}

func pgTypeFacts(catalog CatalogRead, d *typeDescriptor) (sqltype.PGTypeMetadata, error) {
	if d.typ.IsArray() {
		element, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(d.typ.Element().ID())}].(*typeDescriptor)
		if !ok || element.typ != d.typ.Element() || element.array != d.typ.ID() {
			return sqltype.PGTypeMetadata{}, fmt.Errorf("stuntgres: foreign type array authority")
		}
		fact, err := pgTypeFacts(catalog, element)
		if err != nil {
			return fact, err
		}
		return d.typ.PGTypeArrayMetadata(fact), nil
	}
	if d.typ.IsDomain() && d.typ.Concrete() {
		usage := d.typ.DomainBase()
		base, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(usage.Type.ID())}].(*typeDescriptor)
		if !ok || base.typ != usage.Type {
			return sqltype.PGTypeMetadata{}, fmt.Errorf("stuntgres: foreign domain base authority")
		}
		fact, err := pgTypeFacts(catalog, base)
		if err != nil {
			return fact, err
		}
		return d.typ.PGTypeDomainMetadata(fact), nil
	}
	if d.owningRelation != (ids.ObjectAddress{}) {
		relation, ok := catalog.state.descriptors[d.owningRelation].(*relationDescriptor)
		if !ok || relation.rowType != d.typ.ID() {
			return sqltype.PGTypeMetadata{}, fmt.Errorf("stuntgres: foreign row type authority")
		}
		return sqltype.CompositePGTypeMetadata(), nil
	}
	f, ok := d.typ.PGTypeMetadata()
	if !ok {
		return f, fmt.Errorf("stuntgres: missing admitted type metadata")
	}
	if d.typ.IsQueryInt() {
		ext, ok := catalog.state.descriptors[d.extension].(*extensionDescriptor)
		if !ok || len(ext.members) != 3 {
			return f, fmt.Errorf("stuntgres: missing installed type routines")
		}
		f.Input, f.Output = uint32(ext.members[1].ObjectID), uint32(ext.members[2].ObjectID)
	}
	return f, nil
}

// A released stored metadata expression, not executable/inherited-default SQL.
// Its immutable string is grounded by the pinned18.6 discovery scenario.
type pgTypeDefaultTree string

func (s pgTypeDefaultTree) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(s), nil
}

// Caller owns e.mu. Captures own their rows and retain only immutable roots.
func (e *Engine) capturePGType(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.owner != e || catalog.state == nil || source == nil || source.address != (ids.ObjectAddress{ClassID: RelationClassID, ObjectID: pgTypeOID}) || source.relationKind != catalogRelation || ref.Domain() != e.domain || ref.Epoch != catalog.Epoch() || ref.Address != source.address || catalog.state.descriptors[source.address] != source {
		return nil, fmt.Errorf("stuntgres: foreign type source authority")
	}
	if len(source.columns) != len(pgTypeColumnIDs) {
		return nil, fmt.Errorf("stuntgres: invalid type source shape")
	}
	for i, id := range pgTypeColumnIDs {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign type source type")
		}
	}
	database, ok := catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if !ok || database == nil || !roles.BelongsTo(&e.authorizationDomain) || roles.DatabaseOwner() != database.owner {
		return nil, fmt.Errorf("stuntgres: foreign type owner authority")
	}
	count := 0
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d, ok := descriptor.(*typeDescriptor); ok && pgTypeAdmitted(catalog, d) {
			count++
		}
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*len(pgTypeColumnIDs))
	next := 0
	for address, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*typeDescriptor)
		if !ok || !pgTypeAdmitted(catalog, d) {
			continue
		}
		if address != d.Address() {
			return nil, fmt.Errorf("stuntgres: invalid type address authority")
		}
		if err := (typeProvider{}).Validate(catalog, d); err != nil {
			return nil, err
		}
		if _, err := roles.Descriptor(d.owner); err != nil {
			return nil, err
		}
		if d.owner.ID() > math.MaxUint32 {
			return nil, fmt.Errorf("stuntgres: unrepresentable type owner")
		}
		f, err := pgTypeFacts(catalog, d)
		if err != nil {
			return nil, err
		}
		relation := uint32(0)
		if f.Kind == "c" {
			relation = uint32(d.owningRelation.ObjectID)
		}
		numbers := [32]uint32{0: uint32(d.typ.ID()), 2: uint32(d.Namespace().ObjectID), 3: uint32(d.owner.ID()), 11: relation, 12: f.Subscript, 13: uint32(f.Element), 14: uint32(d.array), 15: f.Input, 16: f.Output, 17: f.Receive, 18: f.Send, 19: f.ModIn, 20: f.ModOut, 21: f.Analyze, 25: uint32(f.Base), 28: f.Collation}
		row := values[next*32 : (next+1)*32 : (next+1)*32]
		for i, column := range source.columns {
			switch pgTypeColumnIDs[i] {
			case sqltype.Oid:
				row[i], err = sqltype.OIDValue(column.Usage.Type, numbers[i])
			case sqltype.Regproc:
				row[i], err = sqltype.RegprocValue(column.Usage.Type, numbers[i])
			case sqltype.Int2:
				row[i], err = sqltype.Integer(column.Usage.Type, int64(f.Length))
			case sqltype.Int4:
				n := int64(0)
				if i == 26 {
					n = int64(f.Modifier)
				} else if i == 27 {
					n = int64(d.dimensions)
				}
				row[i], err = sqltype.Integer(column.Usage.Type, n)
			case sqltype.Bool:
				value := false
				switch i {
				case 5:
					value = f.ByValue
				case 8:
					value = f.Preferred
				case 9:
					value = true
				}
				row[i] = sqltype.Boolean(column.Usage.Type, value)
			case sqltype.Name:
				row[i] = sqltype.String(column.Usage.Type, d.Name())
			case sqltype.Char:
				var value string
				switch i {
				case 6:
					value = f.Kind
				case 7:
					value = f.Category
				case 22:
					value = f.Alignment
				case 23:
					value = f.Storage
				default:
					value = string(d.typ.Delimiter())
				}
				row[i] = sqltype.String(column.Usage.Type, value)
			case sqltype.PgNodeTree:
				row[i] = sqltype.Null(column.Usage.Type)
				if f.HasDefaultTree {
					row[i], err = sqltype.Tree(column.Usage.Type, pgTypeDefaultTree(f.DefaultTree))
				}
			case sqltype.Text:
				row[i] = sqltype.Null(column.Usage.Type)
				if f.HasDefault {
					row[i] = sqltype.String(column.Usage.Type, f.Default)
				}
			case sqltype.ACLItemArray:
				var acl authorization.ACLValue
				acl, err = rawObjectACL(catalog, address)
				if err == nil {
					row[i], err = e.pgClassACL(roles, column.Usage.Type, acl)
				}
			}
			if err != nil {
				return nil, err
			}
		}
		rows[next] = row
		next++
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
