package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoUserMappingsOID is the native information-schema relation identity,
// independent of PostgreSQL's allocator and pg_catalog.pg_user_mappings.
const InfoUserMappingsOID ids.OID = 16203

// captureInfoUserMappings reads only the statement's paired immutable catalog
// and roles. Server authority exposes every attached mapping identity; it is
// deliberately distinct from pg_user_mappings option-value disclosure.
func captureInfoUserMappings(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoUserMappingsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 3 {
		return nil, fmt.Errorf("stuntgres: invalid user_mappings source")
	}
	for _, column := range in.Source.Columns {
		typ := column.Usage.Type
		if typ == nil || !typ.IsDomain() || typ.ID() != 13248 || typ.BaseType().ID() != sqltype.Name {
			return nil, fmt.Errorf("stuntgres: invalid user_mappings identifier domain")
		}
	}
	// A nonnil empty slice identifies a captured anonymous source even when
	// there are no mappings or no servers visible to the current principal.
	rows := make([][]sqltype.Value, 0)
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
			return nil, fmt.Errorf("stuntgres: missing user_mappings server")
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
				return nil, fmt.Errorf("stuntgres: missing user_mappings role")
			}
			name = role.Name
		}
		values := [3]string{name, in.Runtime.DatabaseName(), server.Name()}
		row := make([]sqltype.Value, len(values))
		for i, value := range values {
			typ := in.Source.Columns[i].Usage.Type
			row[i], err = typ.Wrap(sqltype.String(typ.BaseType(), value))
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
