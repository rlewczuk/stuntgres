package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoColumnOptionsOID is the native identity, independent of PostgreSQL allocation.
const InfoColumnOptionsOID ids.OID = 16231

var infoColumnOptionsColumns = []ColumnSpec{
	{"table_catalog", 13248, true, 950, 0, -1},
	{"table_schema", 13248, true, 950, 0, -1},
	{"table_name", 13248, true, 950, 0, -1},
	{"column_name", 13248, true, 950, 0, -1},
	{"option_name", 13248, true, 950, 0, -1},
	{"option_value", 13246, true, 950, 0, -1},
}

func captureInfoColumnOptions(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing column_options runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoColumnOptionsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoColumnOptionsColumns) || len(in.Source.Attributes) != len(infoColumnOptionsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid column_options source")
	}
	for i, spec := range infoColumnOptionsColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 5 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoColumnOptionsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid column_options domain authority")
		}
	}
	// Rows and domain carriers are owned by this immutable statement capture.
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
		if len(relation.Attributes) != len(relation.Columns) {
			return nil, fmt.Errorf("stuntgres: invalid column_options attribute layout")
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing column_options namespace")
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
			options := relation.Foreign.Columns[attribute]
			if len(options) == 0 {
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
			for _, option := range options {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				values := [6]string{database, namespace.Name(), relation.Name(), column.Name, option.Name, option.Value}
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
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
