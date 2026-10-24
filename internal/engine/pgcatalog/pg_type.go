package pgcatalog

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"strconv"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const TypeOID ids.OID = 1247

var pgTypeColumnIDs = [...]sqltype.ID{26, 19, 26, 26, 21, 16, 18, 18, 16, 16, 18, 26, 24, 26, 26, 24, 24, 24, 24, 24, 24, 24, 18, 18, 16, 26, 23, 23, 26, 194, 25, 1034}

// Inventory evidence is separate from REGTYPE lookup and codec admission.
// Every companion must be the captured element's canonical array generation.
func pgTypeAdmitted(catalog catalog.Read, d *catalog.Type) bool {
	if d == nil || d.SQLType == nil {
		return false
	}
	if d.SQLType.IsArray() {
		element, ok := catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())})
		return ok && element.SQLType == d.SQLType.Element() && element.Array == d.SQLType.ID() && pgTypeAdmitted(catalog, element)
	}
	if d.SQLType.IsDomain() && d.SQLType.IsUserDefined() {
		// A genuine nominal domain has catalog facts even when its admitted
		// immediate base has metadata but no executable codec.
		usage := d.SQLType.DomainBase()
		base, ok := catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		return ok && base.SQLType == usage.Type && pgTypeAdmitted(catalog, base)
	}
	if d.OwningRelation != (ids.ObjectAddress{}) {
		r, ok := catalog.Relation(d.OwningRelation)
		return ok && r.RowType == d.SQLType.ID()
	}
	_, ok := d.SQLType.PGTypeMetadata()
	return ok
}

func TypeFacts(ctx context.Context, catalog catalog.Read, d *catalog.Type, output sqltype.OutputEnvironment) (sqltype.PGTypeMetadata, error) {
	if d.SQLType.IsArray() {
		element, ok := catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())})
		if !ok || element.SQLType != d.SQLType.Element() || element.Array != d.SQLType.ID() {
			return sqltype.PGTypeMetadata{}, fmt.Errorf("stuntgres: foreign type array authority")
		}
		fact, err := TypeFacts(ctx, catalog, element, output)
		if err != nil {
			return fact, err
		}
		return d.SQLType.PGTypeArrayMetadata(fact), nil
	}
	if d.SQLType.IsDomain() && (d.SQLType.Concrete() || d.SQLType.IsUserDefined()) {
		usage := d.SQLType.DomainBase()
		base, ok := catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if !ok || base.SQLType != usage.Type {
			return sqltype.PGTypeMetadata{}, fmt.Errorf("stuntgres: foreign domain base authority")
		}
		fact, err := TypeFacts(ctx, catalog, base, output)
		if err != nil {
			return fact, err
		}
		result := d.SQLType.PGTypeDomainMetadata(fact)
		result.HasDefault, result.HasDefaultTree = d.DomainDefault != nil, d.DomainDefault != nil
		result.Default = d.DomainDefaultText
		return result, nil
	}
	if d.OwningRelation != (ids.ObjectAddress{}) {
		relation, ok := catalog.Relation(d.OwningRelation)
		if !ok || relation.RowType != d.SQLType.ID() {
			return sqltype.PGTypeMetadata{}, fmt.Errorf("stuntgres: foreign row type authority")
		}
		return sqltype.CompositePGTypeMetadata(), nil
	}
	f, ok := d.SQLType.PGTypeMetadata()
	if !ok {
		return f, fmt.Errorf("stuntgres: missing admitted type metadata")
	}
	if d.SQLType.IsQueryInt() || d.SQLType.IsCube() || d.SQLType.IsCitext() || d.SQLType.IsEan13() || d.SQLType.IsIsbn() || d.SQLType.IsIsbn13() || d.SQLType.IsIsmn() {
		ext, ok := catalog.Extension(d.Extension)
		if !ok || len(ext.Members) < 3 {
			return f, fmt.Errorf("stuntgres: missing installed type routines")
		}
		slot := -1
		for i, address := range ext.Members { if address == d.Address() { slot = i } }
		if slot < 0 || slot+2 >= len(ext.Members) { return f, fmt.Errorf("stuntgres: missing installed scalar role") }
		input, exists := catalog.Procedure(ext.Members[slot+1])
		if !exists || input.Implementation == nil {
			return f, fmt.Errorf("stuntgres: missing installed type provider")
		}
		if err := input.Implementation.ValidateExtension(catalog, ext); err != nil {
			return f, err
		}
		out, exists := catalog.Procedure(ext.Members[slot+2])
		if !exists || input.Signature != "2275" || input.Result != d.Address().ObjectID || out.Signature != catalogSignature(d.Address().ObjectID) || out.Result != 2275 {
			return f, fmt.Errorf("stuntgres: invalid installed type codec relationship")
		}
		f.Input, f.Output = uint32(ext.Members[slot+1].ObjectID), uint32(ext.Members[slot+2].ObjectID)
	}
	return f, nil
}

func catalogSignature(oid ids.OID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(oid), 10))
}

// Released builtin metadata preserves its existing nodeToString witness. User
// defaults use plan.CatalogTree's genuine native structural representation instead.
type pgTypeDefaultTree string

func (s pgTypeDefaultTree) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(s), nil
}

// Caller owns e.mu. Captures own their rows and retain only immutable roots.
func capturePGType(in Input) ([][]sqltype.Value, error) {
	ctx, view, roles, ref, source := in.Context, in.Catalog, in.Roles, in.Ref, in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: TypeOID}) || source.RelationKind != catalog.CatalogRelation || ref.Domain() != in.Owner.Domain || ref.Epoch != view.Epoch() || ref.Address != source.Address() || view.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign type source authority")
	}
	if len(source.Columns) != len(pgTypeColumnIDs) {
		return nil, fmt.Errorf("stuntgres: invalid type source shape")
	}
	for i, id := range pgTypeColumnIDs {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign type source type")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign type owner authority")
	}
	count := 0
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d, ok := descriptor.(*catalog.Type); ok && pgTypeAdmitted(view, d) {
			count++
		}
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*len(pgTypeColumnIDs))
	next := 0
	for address, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Type)
		if !ok || !pgTypeAdmitted(view, d) {
			continue
		}
		if address != d.Address() {
			return nil, fmt.Errorf("stuntgres: invalid type address authority")
		}
		if err := in.Runtime.ValidateType(view, d); err != nil {
			return nil, err
		}
		if _, err := roles.Descriptor(d.Owner); err != nil {
			return nil, err
		}
		if d.Owner.ID() > math.MaxUint32 {
			return nil, fmt.Errorf("stuntgres: unrepresentable type owner")
		}
		f, err := TypeFacts(ctx, view, d, in.Runtime.TypeOutput(in.Owner, view, roles))
		if err != nil {
			return nil, err
		}
		relation := uint32(0)
		if f.Kind == "c" {
			relation = uint32(d.OwningRelation.ObjectID)
		}
		numbers := [32]uint32{0: uint32(d.SQLType.ID()), 2: uint32(d.Namespace().ObjectID), 3: uint32(d.Owner.ID()), 11: relation, 12: f.Subscript, 13: uint32(f.Element), 14: uint32(d.Array), 15: f.Input, 16: f.Output, 17: f.Receive, 18: f.Send, 19: f.ModIn, 20: f.ModOut, 21: f.Analyze, 25: uint32(f.Base), 28: f.Collation}
		row := values[next*32 : (next+1)*32 : (next+1)*32]
		for i, column := range source.Columns {
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
					n = int64(d.Dimensions)
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
				case 24:
					value = d.SQLType.OwnDomainNotNull()
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
					value = string(d.SQLType.Delimiter())
				}
				row[i] = sqltype.String(column.Usage.Type, value)
			case sqltype.PgNodeTree:
				row[i] = sqltype.Null(column.Usage.Type)
				if f.HasDefaultTree {
					if d.DomainDefault != nil && !d.SQLType.IsBuiltin() {
						row[i], err = in.Runtime.ExpressionTree(column.Usage.Type, d.DomainDefault.Expression)
					} else {
						row[i], err = sqltype.Tree(column.Usage.Type, pgTypeDefaultTree(f.DefaultTree))
					}
				}
			case sqltype.Text:
				row[i] = sqltype.Null(column.Usage.Type)
				if f.HasDefault {
					row[i] = sqltype.String(column.Usage.Type, f.Default)
				}
			case sqltype.ACLItemArray:
				var acl authorization.ACLValue
				acl, err = in.Runtime.ObjectACL(view, address)
				if err == nil {
					row[i], err = ClassACL(in.Owner, roles, column.Usage.Type, acl)
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
	return rows, nil
}
