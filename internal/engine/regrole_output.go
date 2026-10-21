package engine

import (
	"stuntgres/internal/engine/exec"
)

func (s *StatementScope) FormatRegrole(id uint32) (string, error) {
	if s == nil {
		return exec.DetachedPresentation{}.FormatRegrole(id)
	}
	return (capturedRegroleOutput{owner: s.inquiry.commentCatalog.Owner(), roles: s.inquiry.commentRoles, quoteAll: s.output.QuoteAllIdentifiers}).FormatRegrole(id)
}
