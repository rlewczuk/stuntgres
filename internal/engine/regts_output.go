package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) formatRegTS(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		if alias == sqltype.Regdictionary {
			return exec.DetachedPresentation{}.FormatRegdictionary(id)
		}
		return exec.DetachedPresentation{}.FormatRegconfig(id)
	}
	return s.presentation().spellings.TextSearch(s.inquiry.commentCatalog, s.inquiry.deparsePath, id, alias), nil
}
func (s *StatementScope) FormatRegconfig(id uint32) (string, error) {
	return s.formatRegTS(id, sqltype.Regconfig)
}
func (s *StatementScope) FormatRegdictionary(id uint32) (string, error) {
	return s.formatRegTS(id, sqltype.Regdictionary)
}
