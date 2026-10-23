package pgcatalog

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const InfoColumnsOID ids.OID = 16214

var infoColumnsColumns = []ColumnSpec{
	{"table_catalog", 13248, true, 950, 0, -1},
	{"table_schema", 13248, true, 950, 0, -1},
	{"table_name", 13248, true, 950, 0, -1},
	{"column_name", 13248, true, 950, 0, -1},
	{"ordinal_position", 13243, true, 0, 0, -1},
	{"column_default", 13246, true, 950, 0, -1},
	{"is_nullable", 13256, true, 950, 0, -1},
	{"data_type", 13246, true, 950, 0, -1},
	{"character_maximum_length", 13243, true, 0, 0, -1},
	{"character_octet_length", 13243, true, 0, 0, -1},
	{"numeric_precision", 13243, true, 0, 0, -1},
	{"numeric_precision_radix", 13243, true, 0, 0, -1},
	{"numeric_scale", 13243, true, 0, 0, -1},
	{"datetime_precision", 13243, true, 0, 0, -1},
	{"interval_type", 13246, true, 950, 0, -1},
	{"interval_precision", 13243, true, 0, 0, -1},
	{"character_set_catalog", 13248, true, 950, 0, -1},
	{"character_set_schema", 13248, true, 950, 0, -1},
	{"character_set_name", 13248, true, 950, 0, -1},
	{"collation_catalog", 13248, true, 950, 0, -1},
	{"collation_schema", 13248, true, 950, 0, -1},
	{"collation_name", 13248, true, 950, 0, -1},
	{"domain_catalog", 13248, true, 950, 0, -1},
	{"domain_schema", 13248, true, 950, 0, -1},
	{"domain_name", 13248, true, 950, 0, -1},
	{"udt_catalog", 13248, true, 950, 0, -1},
	{"udt_schema", 13248, true, 950, 0, -1},
	{"udt_name", 13248, true, 950, 0, -1},
	{"scope_catalog", 13248, true, 950, 0, -1},
	{"scope_schema", 13248, true, 950, 0, -1},
	{"scope_name", 13248, true, 950, 0, -1},
	{"maximum_cardinality", 13243, true, 0, 0, -1},
	{"dtd_identifier", 13248, true, 950, 0, -1},
	{"is_self_referencing", 13256, true, 950, 0, -1},
	{"is_identity", 13256, true, 950, 0, -1},
	{"identity_generation", 13246, true, 950, 0, -1},
	{"identity_start", 13246, true, 950, 0, -1},
	{"identity_increment", 13246, true, 950, 0, -1},
	{"identity_maximum", 13246, true, 950, 0, -1},
	{"identity_minimum", 13246, true, 950, 0, -1},
	{"identity_cycle", 13256, true, 950, 0, -1},
	{"is_generated", 13246, true, 950, 0, -1},
	{"generation_expression", 13246, true, 950, 0, -1},
	{"is_updatable", 13256, true, 950, 0, -1},
}

func captureInfoColumns(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing columns runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoColumnsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || in.Source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: InfoColumnsOID}) || !in.Source.VirtualView || len(in.Source.Columns) != len(infoColumnsColumns) || len(in.Source.Attributes) != len(infoColumnsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid columns source")
	}
	for i, spec := range infoColumnsColumns {
		column := in.Source.Columns[i]
		typ, ok := in.Catalog.LookupType(spec.Type)
		if !ok || !typ.IsDomain() {
			return nil, fmt.Errorf("stuntgres: missing columns domain")
		}
		baseID := sqltype.VarChar
		if spec.Type == 13248 {
			baseID = sqltype.Name
		}
		if spec.Type == 13243 {
			baseID = sqltype.Int4
		}
		base, ok := in.Catalog.LookupType(baseID)
		if !ok || typ.BaseType() != base || column.Name != spec.Name || column.Usage != typ.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != spec.Dimensions || column.OriginTable != uint32(InfoColumnsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid columns domain authority")
		}
	}
	count := 0
	for _, d := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if relation, ok := d.(*catalog.Relation); ok && infoColumnsRelation(in.Catalog, relation) {
			count += len(relation.Columns)
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	database := in.Runtime.DatabaseName()
	for _, d := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := d.(*catalog.Relation)
		if !ok {
			continue
		}
		kind := ClassKind(in.Catalog, relation)
		if kind != "r" && kind != "v" && kind != "f" && kind != "p" {
			continue
		}
		if len(relation.Attributes) != len(relation.Columns) || relation.RelationKind == catalog.CatalogRelation && len(relation.ColumnMeta.CatalogAttributes) != len(relation.Columns) {
			return nil, fmt.Errorf("stuntgres: invalid columns attribute layout")
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing columns namespace")
		}
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != in.Runtime.TemporaryOwner() {
			continue
		}
		owner, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), relation.Owner)
		if err != nil {
			return nil, err
		}
		for slot, column := range relation.Columns {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			attribute := relation.Attributes[slot]
			if attribute <= 0 {
				continue
			}
			if _, dropped := relation.ColumnMeta.Dropped[attribute]; dropped {
				continue
			}
			if !owner {
				address := relation.Address()
				address.SubID = attribute
				rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, address), false)
				if err != nil {
					return nil, err
				}
				if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) == 0 {
					continue
				}
			}
			facts, err := InfoColumnTypeFacts(in.Catalog, column.Usage)
			if err != nil {
				return nil, err
			}
			metadata := AttributeMetadata(relation, slot)
			row := infoColumnsRow{values: make([]sqltype.Value, 44), source: in.Source}
			for i, c := range in.Source.Columns {
				row.values[i] = sqltype.Null(c.Usage.Type)
			}
			row.text(0, database)
			row.text(1, namespace.Name())
			row.text(2, relation.Name())
			row.text(3, column.Name)
			row.number(4, int64(attribute))
			row.text(6, "YES")
			row.text(7, facts.DataType)
			if metadata.NotNull || facts.Domain != nil && facts.Domain.SQLType.OwnDomainNotNull() {
				row.text(6, "NO")
			}
			for i, n := range [6]int64{facts.CharacterMaximumLength, facts.CharacterOctetLength, facts.NumericPrecision, facts.NumericPrecisionRadix, facts.NumericScale, facts.DatetimePrecision} {
				if n >= 0 {
					row.number(8+i, n)
				}
			}
			if facts.IntervalType != "" {
				row.text(14, facts.IntervalType)
			}
			if metadata.Collation != 0 {
				collation, ok := in.Catalog.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(metadata.Collation)})
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing columns collation")
				}
				collationNS, ok := in.Catalog.Namespace(collation.Namespace())
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing columns collation namespace")
				}
				if collationNS.Name() != "pg_catalog" || collation.Name() != "default" {
					row.text(19, database)
					row.text(20, collationNS.Name())
					row.text(21, collation.Name())
				}
			}
			if facts.Domain != nil {
				ns, ok := in.Catalog.Namespace(facts.Domain.Namespace())
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing columns domain namespace")
				}
				row.text(22, database)
				row.text(23, ns.Name())
				row.text(24, facts.Domain.Name())
			}
			udtNS, ok := in.Catalog.Namespace(facts.UDT.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing columns UDT namespace")
			}
			row.text(25, database)
			row.text(26, udtNS.Name())
			row.text(27, facts.UDT.Name())
			row.text(32, strconv.FormatInt(int64(attribute), 10))
			row.text(33, "NO")
			row.text(34, "NO")
			row.text(40, "NO")
			row.text(41, "NEVER")
			if identity, ok := relation.ColumnMeta.Identities[attribute]; ok {
				sequence, ok := in.Catalog.Relation(identity.Sequence)
				if !ok || sequence.Sequence == nil {
					return nil, fmt.Errorf("stuntgres: missing columns identity sequence")
				}
				options := sequence.Sequence
				row.text(34, "YES")
				row.text(35, "BY DEFAULT")
				if identity.Mode == parser.IdentityAlways {
					row.text(35, "ALWAYS")
				}
				row.text(36, strconv.FormatInt(options.Start, 10))
				row.text(37, strconv.FormatInt(options.Increment, 10))
				row.text(38, strconv.FormatInt(options.Max, 10))
				row.text(39, strconv.FormatInt(options.Min, 10))
				if options.Cycle {
					row.text(40, "YES")
				}
			} else if generation, ok := relation.ColumnMeta.Generations[attribute]; ok {
				text, err := in.Runtime.ColumnExpression(in.Context, in.Catalog, relation, generation.Expression)
				if err != nil {
					return nil, err
				}
				row.text(41, "ALWAYS")
				row.text(42, text)
			} else if definition, ok := relation.ColumnMeta.Defaults[attribute]; ok {
				text, err := in.Runtime.ColumnExpression(in.Context, in.Catalog, relation, definition.Expression)
				if err != nil {
					return nil, err
				}
				row.text(5, text)
			}
			updatable := kind == "r" || kind == "p"
			if !updatable {
				updatable, err = in.Runtime.ColumnUpdatable(in.Context, in.Catalog, relation, attribute)
				if err != nil {
					return nil, err
				}
			}
			row.text(43, "NO")
			if updatable {
				row.text(43, "YES")
			}
			if row.err != nil {
				return nil, row.err
			}
			rows = append(rows, row.values)
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func infoColumnsRelation(view catalog.Read, relation *catalog.Relation) bool {
	switch ClassKind(view, relation) {
	case "r", "v", "f", "p":
		return true
	}
	return false
}

type infoColumnsRow struct {
	values []sqltype.Value
	source *catalog.Relation
	err    error
}

func (r *infoColumnsRow) text(i int, text string) {
	if r.err != nil {
		return
	}
	typ := r.source.Columns[i].Usage.Type
	r.values[i], r.err = typ.Wrap(sqltype.String(typ.BaseType(), text))
}
func (r *infoColumnsRow) number(i int, n int64) {
	if r.err != nil {
		return
	}
	typ := r.source.Columns[i].Usage.Type
	value, err := sqltype.Integer(typ.BaseType(), n)
	if err != nil {
		r.err = err
		return
	}
	r.values[i], r.err = typ.Wrap(value)
}
