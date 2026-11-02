package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoApplicableRolesOID is the native relation identity, independent of
// PostgreSQL's allocated information-schema relation OID.
const InfoApplicableRolesOID ids.OID = 16257

func captureInfoApplicableRoles(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoApplicableRolesOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 3 {
		return nil, fmt.Errorf("stuntgres: invalid applicable_roles source")
	}
	var domains, bases [3]*sqltype.Descriptor
	names := [...]string{"grantee", "role_name", "is_grantable"}
	for i, id := range [...]sqltype.ID{13248, 13248, 13256} {
		baseID, typmod := sqltype.Name, int32(-1)
		if i == 2 {
			baseID, typmod = sqltype.VarChar, 7
		}
		domain, ok := in.Catalog.LookupType(id)
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || domain.DomainBase().Typmod != typmod || in.Source.Columns[i].Name != names[i] || in.Source.Columns[i].Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid applicable_roles domain")
		}
		domains[i], bases[i] = domain, base
	}
	actor, err := in.Roles.Descriptor(in.Runtime.CurrentPrincipal())
	if err != nil {
		return nil, err
	}
	var visible map[authorization.PrincipalRef]bool
	if !actor.Superuser {
		closure, err := in.Roles.RoleClosure(ctx, actor.Ref, true)
		if err != nil {
			return nil, err
		}
		visible = make(map[authorization.PrincipalRef]bool, len(closure))
		for _, ref := range closure {
			visible[ref] = true
		}
	}
	type triple struct {
		member, role authorization.PrincipalRef
		admin        bool
	}
	seen := make(map[triple]bool)
	var cells []sqltype.Value
	emit := func(member, role authorization.PrincipalRef, admin bool) error {
		// Hidden and duplicate edges are cancellation boundaries too.
		if err := ctx.Err(); err != nil {
			return err
		}
		grantee, err := in.Roles.Descriptor(member)
		if err != nil {
			return err
		}
		target, err := in.Roles.Descriptor(role)
		if err != nil {
			return err
		}
		if !actor.Superuser && !visible[member] {
			return nil
		}
		key := triple{member, role, admin}
		if seen[key] {
			return nil
		}
		seen[key] = true
		grantable := "NO"
		if admin {
			grantable = "YES"
		}
		for i, text := range [...]string{grantee.Name, target.Name, grantable} {
			value, err := domains[i].Wrap(sqltype.String(bases[i], text))
			if err != nil {
				return err
			}
			cells = append(cells, value)
		}
		return nil
	}
	// USAGE filters the grantee, not the edge's target or its INHERIT/SET flags.
	// UNION's key excludes the grantor but includes the actual ADMIN flag.
	for edge := range in.Roles.Edges() {
		if err := emit(edge.Member, edge.Role, edge.Admin); err != nil {
			return nil, err
		}
	}
	ownerRole, ok := in.Roles.Lookup("pg_database_owner")
	if !ok || ownerRole.Ref.ID() != authorization.DatabaseOwnerRoleID || !ownerRole.Predefined() {
		return nil, fmt.Errorf("stuntgres: missing applicable_roles database owner role")
	}
	if err := emit(in.Roles.DatabaseOwner(), ownerRole.Ref, false); err != nil {
		return nil, err
	}
	// Publish one flat backing only after a complete successful capture. Empty
	// ordinary views retain a nonnil row slice without allocating for hidden edges.
	rows := make([][]sqltype.Value, len(cells)/3)
	for i := range rows {
		at := i * 3
		rows[i] = cells[at : at+3 : at+3]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
