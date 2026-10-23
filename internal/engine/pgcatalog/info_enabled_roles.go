package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoEnabledRolesOID is the native information-schema relation identity,
// independent of PostgreSQL's allocated relation OID.
const InfoEnabledRolesOID ids.OID = 16208

func captureInfoEnabledRoles(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoEnabledRolesOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid enabled_roles source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	base, baseOK := in.Catalog.LookupType(sqltype.Name)
	if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || in.Source.Columns[0].Usage.Type != domain {
		return nil, fmt.Errorf("stuntgres: invalid enabled_roles identifier domain")
	}
	actor, err := in.Roles.Descriptor(in.Runtime.CurrentPrincipal())
	if err != nil {
		return nil, err
	}
	var closure []authorization.PrincipalRef
	count := in.Roles.RoleCount()
	if !actor.Superuser {
		closure, err = in.Roles.RoleClosure(in.Context, actor.Ref, true)
		if err != nil {
			return nil, err
		}
		count = len(closure)
	}
	rows := make([][]sqltype.Value, count)
	cells := make([]sqltype.Value, count)
	at := 0
	emit := func(name string) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		value, err := domain.Wrap(sqltype.String(base, name))
		if err != nil {
			return err
		}
		cells[at] = value
		rows[at] = cells[at : at+1 : at+1]
		at++
		return nil
	}
	// pg_has_role(oid, 'USAGE') grants an actual current superuser access to
	// every live role. Membership alone never inherits the SUPERUSER attribute.
	if actor.Superuser {
		for _, role := range in.Roles.RoleEntries() {
			if err := emit(role.Name); err != nil {
				return nil, err
			}
		}
	} else {
		for _, ref := range closure {
			role, err := in.Roles.Descriptor(ref)
			if err != nil {
				return nil, err
			}
			if err := emit(role.Name); err != nil {
				return nil, err
			}
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
