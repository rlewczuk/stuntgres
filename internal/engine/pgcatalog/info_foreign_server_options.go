package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoForeignServerOptionsOID is a native identity, not a PostgreSQL allocation.
const InfoForeignServerOptionsOID ids.OID = 16245

var infoForeignServerOptionsColumns = []ColumnSpec{
	{"foreign_server_catalog", 13248, true, 950, 0, -1},
	{"foreign_server_name", 13248, true, 950, 0, -1},
	{"option_name", 13248, true, 950, 0, -1},
	{"option_value", 13246, true, 950, 0, -1},
}

func captureInfoForeignServerOptions(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing foreign_server_options runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoForeignServerOptionsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoForeignServerOptionsColumns) || len(in.Source.Attributes) != len(infoForeignServerOptionsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid foreign_server_options source")
	}
	if in.Source.Name() != "foreign_server_options" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || in.Source.Address().ClassID != ids.RelationClassID {
		return nil, fmt.Errorf("stuntgres: invalid foreign_server_options identity")
	}
	for i, spec := range infoForeignServerOptionsColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 3 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoForeignServerOptionsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid foreign_server_options domain authority")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		server, ok := descriptor.(*catalog.ForeignObject)
		if !ok || server.Address().ClassID != ids.ForeignServerClassID {
			continue
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
		// PostgreSQL discloses every configured value to an eligible server reader.
		for _, option := range server.Options {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			values := [4]string{database, server.Name(), option.Name, option.Value}
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
