package regobj

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type ACLItemInput struct {
	Roles     authorization.Read
	Authority *sqltype.ACLAuthority
	Op        *session.Operation
	notice    typeNotices // Captured evaluator sink; ordinary input uses Op.
	position  int
	portal    string
}

func (r ACLItemInput) ACLItemAuthority() *sqltype.ACLAuthority { return r.Authority }

func (r ACLItemInput) ResolveACLRole(ctx context.Context, name string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	role, ok := r.Roles.Lookup(name)
	if !ok {
		return 0, sqlstate.SoftInputError(sqlError("42704", `role "`+name+`" does not exist`))
	}
	if role.Ref.ID() > math.MaxUint32 {
		return 0, fmt.Errorf("stuntgres: ACL role identity out of range")
	}
	return uint32(role.Ref.ID()), nil
}

func (r ACLItemInput) WarnACLGrantor(ctx context.Context) {
	if (r.Op == nil && r.notice == nil) || ctx.Err() != nil {
		return
	}
	warning := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0L000", Message: "defaulting grantor to user ID 10", Context: r.portal}
	if r.position > 0 {
		warning.Position = strconv.Itoa(r.position)
	}
	if r.notice != nil {
		r.notice(session.LevelWarning, warning)
	} else {
		r.Op.EmitNotice(session.LevelWarning, warning)
	}
}

func (r ACLItemInput) ACLItemParameter(slot int, empty bool) sqltype.ACLItemResolver {
	r.position = 0
	r.portal = fmt.Sprintf("unnamed portal parameter $%d = '...'", slot)
	if empty {
		r.portal = fmt.Sprintf("unnamed portal parameter $%d = ''", slot)
	}
	return r
}

func (r ACLItemInput) ACLItemLiteral(position int) sqltype.ACLItemResolver {
	r.position = position
	r.portal = ""
	return r
}
