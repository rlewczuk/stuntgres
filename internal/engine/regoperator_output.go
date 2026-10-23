package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) formatOperator(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		if alias == sqltype.Regoperator {
			return exec.DetachedPresentation{}.FormatRegoperator(id)
		}
		return exec.DetachedPresentation{}.FormatRegoper(id)
	}
	return s.presentation().spellings.Operator(s.inquiry.commentCatalog, s.inquiry.deparsePath, id, alias), nil
}
func (s *StatementScope) FormatRegoper(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoper)
}
func (s *StatementScope) FormatRegoperator(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoperator)
}
