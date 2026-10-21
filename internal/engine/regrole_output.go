package engine

import (
	"stuntgres/internal/engine/exec"
)

func (s *StatementScope) FormatRegrole(id uint32) (string, error) {
	if s == nil {
		return exec.DetachedPresentation{}.FormatRegrole(id)
	}
	return (capturedRegroleOutput{Owner: s.inquiry.commentCatalog.Owner(), Roles: s.inquiry.commentRoles, QuoteAll: s.output.QuoteAllIdentifiers}).FormatRegrole(id)
}
