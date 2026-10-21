package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.Owner() == nil {
		return exec.DetachedPresentation{}.FormatACLRole(authority, id)
	}
	return (capturedACLItemOutput{owner: s.inquiry.commentCatalog.Owner(), roles: s.inquiry.commentRoles}).FormatACLRole(authority, id)
}
