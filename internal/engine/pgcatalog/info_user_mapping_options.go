package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoUserMappingOptionsOID is the native identity, independent of PostgreSQL allocation.
const InfoUserMappingOptionsOID ids.OID = 16235

var infoUserMappingOptionsColumns = []ColumnSpec{
	{"authorization_identifier", 13248, true, 950, 0, -1},
	{"foreign_server_catalog", 13248, true, 950, 0, -1},
	{"foreign_server_name", 13248, true, 950, 0, -1},
	{"option_name", 13248, true, 950, 0, -1},
	{"option_value", 13246, true, 950, 0, -1},
}

func captureInfoUserMappingOptions(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing user_mapping_options runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoUserMappingOptionsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoUserMappingOptionsColumns) || len(in.Source.Attributes) != len(infoUserMappingOptionsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid user_mapping_options source")
	}
	for i, spec := range infoUserMappingOptionsColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 4 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoUserMappingOptionsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid user_mapping_options domain authority")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		mapping, ok := descriptor.(*catalog.ForeignObject)
		if !ok || mapping.Address().ClassID != ids.ForeignMappingClassID {
			continue
		}
		server, ok := in.Catalog.Foreign(mapping.Server)
		if !ok || server.Address().ClassID != ids.ForeignServerClassID {
			return nil, fmt.Errorf("stuntgres: missing user_mapping_options server")
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), server.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, server.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.UsagePrivilege == 0 {
				continue
			}
		}
		name := "PUBLIC"
		if mapping.MappedRole != (authorization.PrincipalRef{}) {
			role, ok := in.Roles.Find(mapping.MappedRole)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing user_mapping_options role")
			}
			name = role.Name
		}
		// Server access exposes option identities, not another user's option values.
		disclose, err := in.Runtime.ForeignServerVisible(server, mapping.MappedRole)
		if err != nil {
			return nil, err
		}
		for _, option := range mapping.Options {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			values := [5]string{name, database, server.Name(), option.Name, option.Value}
			row := make([]sqltype.Value, len(values))
			for i, value := range values {
				domain := in.Source.Columns[i].Usage.Type
				if i == 4 && !disclose {
					row[i] = sqltype.Null(domain)
					continue
				}
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
