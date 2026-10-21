package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/regobj"
)

func (s *StatementScope) FormatRegrole(id uint32) (string, error) {
	if s == nil {
		return exec.DetachedPresentation{}.FormatRegrole(id)
	}
	return (regobj.RoleOutput{Owner: s.inquiry.commentCatalog.Owner(), Roles: s.inquiry.commentRoles, QuoteAll: s.output.QuoteAllIdentifiers}).FormatRegrole(id)
}
