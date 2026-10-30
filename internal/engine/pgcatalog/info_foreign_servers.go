package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoForeignServersOID is a native identity, not a PostgreSQL allocation.
const InfoForeignServersOID ids.OID = 16246

var infoForeignServersColumns = []ColumnSpec{
	{"foreign_server_catalog", 13248, true, 950, 0, -1},
	{"foreign_server_name", 13248, true, 950, 0, -1},
	{"foreign_data_wrapper_catalog", 13248, true, 950, 0, -1},
	{"foreign_data_wrapper_name", 13248, true, 950, 0, -1},
	{"foreign_server_type", 13246, true, 950, 0, -1},
	{"foreign_server_version", 13246, true, 950, 0, -1},
	{"authorization_identifier", 13248, true, 950, 0, -1},
}

func captureInfoForeignServers(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing foreign_servers runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoForeignServersOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoForeignServersColumns) || len(in.Source.Attributes) != len(infoForeignServersColumns) {
		return nil, fmt.Errorf("stuntgres: invalid foreign_servers source")
	}
	if in.Source.Name() != "foreign_servers" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || in.Source.Address().ClassID != ids.RelationClassID {
		return nil, fmt.Errorf("stuntgres: invalid foreign_servers identity")
	}
	for i, spec := range infoForeignServersColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 4 || i == 5 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoForeignServersOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid foreign_servers domain authority")
		}
	}
	identifier := in.Source.Columns[0].Usage.Type
	characterData := in.Source.Columns[4].Usage.Type
	database, err := identifier.Wrap(sqltype.String(identifier.BaseType(), in.Runtime.DatabaseName()))
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		server, ok := descriptor.(*catalog.ForeignObject)
		if !ok || server.Address().ClassID != ids.ForeignServerClassID {
			continue
		}
		wrapper, ok := in.Catalog.Foreign(server.Wrapper)
		if !ok || wrapper.Address().ClassID != ids.ForeignWrapperClassID {
			return nil, fmt.Errorf("stuntgres: invalid foreign_servers wrapper authority")
		}
		owner, err := in.Roles.Descriptor(server.Owner)
		if err != nil {
			return nil, err
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
		name, err := identifier.Wrap(sqltype.String(identifier.BaseType(), server.Name()))
		if err != nil {
			return nil, err
		}
		ownerName, err := identifier.Wrap(sqltype.String(identifier.BaseType(), owner.Name))
		if err != nil {
			return nil, err
		}
		wrapperName, err := identifier.Wrap(sqltype.String(identifier.BaseType(), wrapper.Name()))
		if err != nil {
			return nil, err
		}
		kind, version := sqltype.Null(characterData), sqltype.Null(characterData)
		if server.ServerType != nil {
			kind, err = characterData.Wrap(sqltype.String(characterData.BaseType(), *server.ServerType))
			if err != nil {
				return nil, err
			}
		}
		if server.Version != nil {
			version, err = characterData.Wrap(sqltype.String(characterData.BaseType(), *server.Version))
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, []sqltype.Value{database, name, database, wrapperName, kind, version, ownerName})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
