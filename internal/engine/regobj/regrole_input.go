package regobj

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type RoleInput struct{ Roles authorization.Read }

func (r RoleInput) ResolveRegrole(ctx context.Context, input string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parts, err := parser.ParseRegclassName(input)
	if err != nil || len(parts.Parts) != 1 {
		return 0, sqlstate.SoftInputError(sqlError("42602", "invalid name syntax"))
	}
	name := parts.Parts[0].Text
	role, ok := r.Roles.Lookup(name)
	if !ok {
		return 0, sqlstate.SoftInputError(sqlError("42704", `role "`+name+`" does not exist`))
	}
	if role.Ref.ID() > math.MaxUint32 {
		return 0, fmt.Errorf("stuntgres: regrole identity out of range")
	}
	return uint32(role.Ref.ID()), nil
}

// AccessRoleID is the principal ID of a role argument of the ACL inquiry
// functions: its OID as given, or the role its name finds in roles.
func AccessRoleID(roles authorization.Read, role functions.AccessRole) (uint64, error) {
	if role.ByOID {
		return uint64(role.OID), nil
	}
	found, ok := roles.Lookup(role.Name)
	if !ok {
		return 0, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, role.Name))
	}
	return found.Ref.ID(), nil
}
