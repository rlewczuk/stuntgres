package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoForeignDataWrapperOptionsOID is a native identity, not a PostgreSQL allocation.
const InfoForeignDataWrapperOptionsOID ids.OID = 16243

var infoForeignDataWrapperOptionsColumns = []ColumnSpec{
	{"foreign_data_wrapper_catalog", 13248, true, 950, 0, -1},
	{"foreign_data_wrapper_name", 13248, true, 950, 0, -1},
	{"option_name", 13248, true, 950, 0, -1},
	{"option_value", 13246, true, 950, 0, -1},
}

func captureInfoForeignDataWrapperOptions(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing foreign_data_wrapper_options runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoForeignDataWrapperOptionsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoForeignDataWrapperOptionsColumns) || len(in.Source.Attributes) != len(infoForeignDataWrapperOptionsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid foreign_data_wrapper_options source")
	}
	if in.Source.Name() != "foreign_data_wrapper_options" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || in.Source.Address().ClassID != ids.RelationClassID {
		return nil, fmt.Errorf("stuntgres: invalid foreign_data_wrapper_options identity")
	}
	for i, spec := range infoForeignDataWrapperOptionsColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 3 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoForeignDataWrapperOptionsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid foreign_data_wrapper_options domain authority")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		wrapper, ok := descriptor.(*catalog.ForeignObject)
		if !ok || wrapper.Address().ClassID != ids.ForeignWrapperClassID {
			continue
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), wrapper.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, wrapper.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.UsagePrivilege == 0 {
				continue
			}
		}
		// PostgreSQL discloses every configured value to an eligible wrapper reader.
		for _, option := range wrapper.Options {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			values := [4]string{database, wrapper.Name(), option.Name, option.Value}
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
