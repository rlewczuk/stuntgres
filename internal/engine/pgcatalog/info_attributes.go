package pgcatalog

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoAttributesOID is a native relation identity, not a PostgreSQL allocation.
const InfoAttributesOID ids.OID = 16258

var infoAttributesColumns = []ColumnSpec{
	{"udt_catalog", 13248, true, 950, 0, -1},
	{"udt_schema", 13248, true, 950, 0, -1},
	{"udt_name", 13248, true, 950, 0, -1},
	{"attribute_name", 13248, true, 950, 0, -1},
	{"ordinal_position", 13243, true, 0, 0, -1},
	{"attribute_default", 13246, true, 950, 0, -1},
	{"is_nullable", 13256, true, 950, 0, -1},
	{"data_type", 13246, true, 950, 0, -1},
	{"character_maximum_length", 13243, true, 0, 0, -1},
	{"character_octet_length", 13243, true, 0, 0, -1},
	{"character_set_catalog", 13248, true, 950, 0, -1},
	{"character_set_schema", 13248, true, 950, 0, -1},
	{"character_set_name", 13248, true, 950, 0, -1},
	{"collation_catalog", 13248, true, 950, 0, -1},
	{"collation_schema", 13248, true, 950, 0, -1},
	{"collation_name", 13248, true, 950, 0, -1},
	{"numeric_precision", 13243, true, 0, 0, -1},
	{"numeric_precision_radix", 13243, true, 0, 0, -1},
	{"numeric_scale", 13243, true, 0, 0, -1},
	{"datetime_precision", 13243, true, 0, 0, -1},
	{"interval_type", 13246, true, 950, 0, -1},
	{"interval_precision", 13243, true, 0, 0, -1},
	{"attribute_udt_catalog", 13248, true, 950, 0, -1},
	{"attribute_udt_schema", 13248, true, 950, 0, -1},
	{"attribute_udt_name", 13248, true, 950, 0, -1},
	{"scope_catalog", 13248, true, 950, 0, -1},
	{"scope_schema", 13248, true, 950, 0, -1},
	{"scope_name", 13248, true, 950, 0, -1},
	{"maximum_cardinality", 13243, true, 0, 0, -1},
	{"dtd_identifier", 13248, true, 950, 0, -1},
	{"is_derived_reference_attribute", 13256, true, 950, 0, -1},
}

// Caller owns the capture lock; every authority belongs to the admitted frontier.
func captureInfoAttributes(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing attributes runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoAttributesOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || in.Source.Name() != "attributes" || in.Source.Namespace().ObjectID != 13229 || in.Source.RelationKind != catalog.CatalogRelation || !in.Source.VirtualView || len(in.Source.Columns) != len(infoAttributesColumns) || len(in.Source.Attributes) != len(infoAttributesColumns) || len(in.Source.ColumnMeta.CatalogAttributes) != len(infoAttributesColumns) {
		return nil, fmt.Errorf("stuntgres: invalid attributes source")
	}
	for i, spec := range infoAttributesColumns {
		column := in.Source.Columns[i]
		typ, ok := in.Catalog.LookupType(spec.Type)
		if !ok || !typ.IsDomain() || typ.OwnDomainNotNull() || column.Name != spec.Name || column.Usage != typ.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != spec.Dimensions || column.OriginTable != uint32(InfoAttributesOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid attributes source domain")
		}
		fact := in.Source.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull || fact.Dimensions != spec.Dimensions || fact.Collation != spec.Collation {
			return nil, fmt.Errorf("stuntgres: invalid attributes source attribute facts")
		}
		base := typ.DomainBase()
		current, ok := in.Catalog.LookupType(base.Type.ID())
		expected, mod, collation := sqltype.Name, int32(-1), uint32(950)
		switch spec.Type {
		case 13246:
			expected = sqltype.VarChar
		case 13256:
			expected, mod = sqltype.VarChar, 7
		case 13243:
			expected, collation = sqltype.Int4, 0
		}
		if !ok || base.Type != current || base.Type.ID() != expected || base.Typmod != mod || base.Collation.ID != collation {
			return nil, fmt.Errorf("stuntgres: foreign attributes source base")
		}
	}
	var rows [][]sqltype.Value
	database := in.Runtime.DatabaseName()
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.CompositeRelation {
			continue
		}
		if relation.Address() != address || len(relation.Attributes) != len(relation.Columns) {
			return nil, fmt.Errorf("stuntgres: invalid attributes relation layout")
		}
		typ, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
		if !ok || !typ.SQLType.IsComposite() || typ.OwningRelation != address || typ.Namespace() != relation.Namespace() || typ.Name() != relation.Name() || typ.Owner != relation.Owner {
			return nil, fmt.Errorf("stuntgres: invalid attributes reciprocal type")
		}
		if err := in.Runtime.ValidateType(in.Catalog, typ); err != nil {
			return nil, err
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok || namespace.Address() != relation.Namespace() {
			return nil, fmt.Errorf("stuntgres: invalid attributes namespace")
		}
		if err := (catalog.NamespaceProvider{}).Validate(in.Catalog, namespace); err != nil {
			return nil, err
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), typ.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, typ.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.UsagePrivilege == 0 {
				continue
			}
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
			facts, err := InfoColumnTypeFacts(in.Catalog, column.Usage)
			if err != nil {
				return nil, err
			}
			declared, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())})
			if !ok || declared.SQLType != column.Usage.Type {
				return nil, fmt.Errorf("stuntgres: invalid attributes declared type")
			}
			if err := in.Runtime.ValidateType(in.Catalog, declared); err != nil {
				return nil, err
			}
			typeNS, ok := in.Catalog.Namespace(declared.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing attributes type namespace")
			}
			if err := (catalog.NamespaceProvider{}).Validate(in.Catalog, typeNS); err != nil {
				return nil, err
			}
			dataType := "USER-DEFINED"
			if declared.SQLType.IsArray() {
				dataType = "ARRAY"
			} else if typeNS.Address().ObjectID == ids.PGCatalogNamespaceOID {
				dataType = declared.SQLType.DiagnosticName()
				if dataType == "" {
					dataType = declared.Name()
				}
			}
			metadata := AttributeMetadata(relation, slot)
			row := infoColumnsRow{values: make([]sqltype.Value, len(infoAttributesColumns)), source: in.Source}
			for i, sourceColumn := range in.Source.Columns {
				row.values[i] = sqltype.Null(sourceColumn.Usage.Type)
			}
			row.text(0, database)
			row.text(1, namespace.Name())
			row.text(2, relation.Name())
			row.text(3, column.Name)
			row.number(4, int64(attribute))
			row.text(6, "YES")
			if metadata.NotNull || declared.SQLType.IsDomain() && declared.SQLType.OwnDomainNotNull() {
				row.text(6, "NO")
			}
			row.text(7, dataType)
			for i, number := range [6]int64{facts.CharacterMaximumLength, facts.CharacterOctetLength, facts.NumericPrecision, facts.NumericPrecisionRadix, facts.NumericScale, facts.DatetimePrecision} {
				if number >= 0 {
					row.number([6]int{8, 9, 16, 17, 18, 19}[i], number)
				}
			}
			if facts.IntervalType != "" {
				row.text(20, facts.IntervalType)
			}
			if metadata.Collation != 0 {
				collation, ok := in.Catalog.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(metadata.Collation)})
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing attributes collation")
				}
				collationNS, ok := in.Catalog.Namespace(collation.Namespace())
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing attributes collation namespace")
				}
				if err := (catalog.CollationProvider{}).Validate(in.Catalog, collation); err != nil {
					return nil, err
				}
				if err := (catalog.NamespaceProvider{}).Validate(in.Catalog, collationNS); err != nil {
					return nil, err
				}
				if collationNS.Name() != "pg_catalog" || collation.Name() != "default" {
					row.text(13, database)
					row.text(14, collationNS.Name())
					row.text(15, collation.Name())
				}
			}
			if definition, ok := relation.ColumnMeta.Defaults[attribute]; ok {
				text, err := in.Runtime.ColumnExpression(in.Context, in.Catalog, relation, definition.Expression)
				if err != nil {
					return nil, err
				}
				row.text(5, text)
			}
			row.text(22, database)
			row.text(23, typeNS.Name())
			row.text(24, declared.Name())
			row.text(29, strconv.FormatInt(int64(attribute), 10))
			row.text(30, "NO")
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
