package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.Owner() == nil {
		return exec.DetachedPresentation{}.FormatACLRole(authority, id)
	}
	return (regobj.ACLItemOutput{Owner: s.Inquiry.CommentCatalog.Owner(), Roles: s.Inquiry.commentRoles}).FormatACLRole(authority, id)
}
