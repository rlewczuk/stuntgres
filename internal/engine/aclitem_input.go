package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type capturedACLItemInput struct {
	roles     authorization.Read
	authority *sqltype.ACLAuthority
	op        *Operation
	position  int
	portal    string
}

func (e *Engine) captureACLItemInput(auth AuthorizationView, op *Operation) capturedACLItemInput {
	return capturedACLItemInput{roles: auth.Roles, authority: &e.roles.aclAuthority, op: op}
}
func (r capturedACLItemInput) ACLItemAuthority() *sqltype.ACLAuthority { return r.authority }
func (r capturedACLItemInput) ResolveACLRole(ctx context.Context, name string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	role, ok := r.roles.Lookup(name)
	if !ok {
		return 0, sqlError("42704", `role "`+name+`" does not exist`)
	}
	if role.Ref.ID() > math.MaxUint32 {
		return 0, fmt.Errorf("stuntgres: ACL role identity out of range")
	}
	return uint32(role.Ref.ID()), nil
}
func (r capturedACLItemInput) WarnACLGrantor(ctx context.Context) {
	if r.op == nil || ctx.Err() != nil {
		return
	}
	warning := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0L000", Message: "defaulting grantor to user ID 10", Context: r.portal}
	if r.position > 0 {
		warning.Position = strconv.Itoa(r.position)
	}
	r.op.EmitNotice(levelWarning, warning)
}
func (r capturedACLItemInput) ACLItemParameter(slot int, empty bool) sqltype.ACLItemResolver {
	r.position = 0
	r.portal = fmt.Sprintf("unnamed portal parameter $%d = '...'", slot)
	if empty {
		r.portal = fmt.Sprintf("unnamed portal parameter $%d = ''", slot)
	}
	return r
}
func (r capturedACLItemInput) ACLItemLiteral(position int) sqltype.ACLItemResolver {
	r.position = position
	r.portal = ""
	return r
}
