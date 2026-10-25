package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/regobj"
)

func (s *StatementScope) FormatRegrole(id uint32) (string, error) {
	if s == nil {
		return exec.DetachedPresentation{}.FormatRegrole(id)
	}
	return (regobj.RoleOutput{Owner: s.Inquiry.CommentCatalog.Owner(), Roles: s.Inquiry.commentRoles, QuoteAll: s.Output.QuoteAllIdentifiers}).FormatRegrole(id)
}
