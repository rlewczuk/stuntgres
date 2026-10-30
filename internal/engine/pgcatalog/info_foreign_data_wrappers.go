package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoForeignDataWrappersOID is a native identity, not a PostgreSQL allocation.
const InfoForeignDataWrappersOID ids.OID = 16244

var infoForeignDataWrappersColumns = []ColumnSpec{
	{"foreign_data_wrapper_catalog", 13248, true, 950, 0, -1},
	{"foreign_data_wrapper_name", 13248, true, 950, 0, -1},
	{"authorization_identifier", 13248, true, 950, 0, -1},
	{"library_name", 13246, true, 950, 0, -1},
	{"foreign_data_wrapper_language", 13246, true, 950, 0, -1},
}

func captureInfoForeignDataWrappers(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing foreign_data_wrappers runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoForeignDataWrappersOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoForeignDataWrappersColumns) || len(in.Source.Attributes) != len(infoForeignDataWrappersColumns) {
		return nil, fmt.Errorf("stuntgres: invalid foreign_data_wrappers source")
	}
	if in.Source.Name() != "foreign_data_wrappers" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || in.Source.Address().ClassID != ids.RelationClassID {
		return nil, fmt.Errorf("stuntgres: invalid foreign_data_wrappers identity")
	}
	for i, spec := range infoForeignDataWrappersColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i >= 3 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoForeignDataWrappersOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid foreign_data_wrappers domain authority")
		}
	}
	identifier := in.Source.Columns[0].Usage.Type
	characterData := in.Source.Columns[3].Usage.Type
	database, err := identifier.Wrap(sqltype.String(identifier.BaseType(), in.Runtime.DatabaseName()))
	if err != nil {
		return nil, err
	}
	language, err := characterData.Wrap(sqltype.String(characterData.BaseType(), "c"))
	if err != nil {
		return nil, err
	}
	library := sqltype.Null(characterData)
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		wrapper, ok := descriptor.(*catalog.ForeignObject)
		if !ok || wrapper.Address().ClassID != ids.ForeignWrapperClassID {
			continue
		}
		owner, err := in.Roles.Descriptor(wrapper.Owner)
		if err != nil {
			return nil, err
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
		name, err := identifier.Wrap(sqltype.String(identifier.BaseType(), wrapper.Name()))
		if err != nil {
			return nil, err
		}
		ownerName, err := identifier.Wrap(sqltype.String(identifier.BaseType(), owner.Name))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{database, name, ownerName, library, language})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
