package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoForeignTableOptionsOID is a native identity, not a PostgreSQL allocation.
const InfoForeignTableOptionsOID ids.OID = 16247

var infoForeignTableOptionsColumns = []ColumnSpec{
	{"foreign_table_catalog", 13248, true, 950, 0, -1},
	{"foreign_table_schema", 13248, true, 950, 0, -1},
	{"foreign_table_name", 13248, true, 950, 0, -1},
	{"option_name", 13248, true, 950, 0, -1},
	{"option_value", 13246, true, 950, 0, -1},
}

func captureInfoForeignTableOptions(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing foreign_table_options runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoForeignTableOptionsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoForeignTableOptionsColumns) || len(in.Source.Attributes) != len(infoForeignTableOptionsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid foreign_table_options source")
	}
	if in.Source.Name() != "foreign_table_options" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || in.Source.Address().ClassID != ids.RelationClassID {
		return nil, fmt.Errorf("stuntgres: invalid foreign_table_options identity")
	}
	for i, spec := range infoForeignTableOptionsColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 4 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoForeignTableOptionsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid foreign_table_options domain authority")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.ForeignRelation || relation.Foreign == nil {
			continue
		}
		_, visible, err := infoRelationVisible(in, relation)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing foreign_table_options namespace")
		}
		// Eligibility is whole-table: every configured key/value remains intact.
		for _, option := range relation.Foreign.Options {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			values := [5]string{database, namespace.Name(), relation.Name(), option.Name, option.Value}
			row := make([]sqltype.Value, len(values))
			for i, value := range values {
				domain := in.Source.Columns[i].Usage.Type
				row[i], err = domain.Wrap(sqltype.String(domain.BaseType(), value))
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
