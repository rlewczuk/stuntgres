package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) formatOperator(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		if alias == sqltype.Regoperator {
			return exec.DetachedPresentation{}.FormatRegoperator(id)
		}
		return exec.DetachedPresentation{}.FormatRegoper(id)
	}
	key := exec.OperatorOutputKey{ID: id, Alias: alias}
	if text, ok := s.presentationOrZero().operators[key]; ok {
		return text, nil
	}
	if s.presentationOrZero().operatorGroups == nil {
		s.presentation().operatorGroups = collectOperatorGroups(s.inquiry.commentCatalog)
	}
	text := operatorSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, s.presentationOrZero().operatorGroups, ids.OID(id), alias)
	if s.presentationOrZero().operators == nil {
		s.presentation().operators = make(map[exec.OperatorOutputKey]string)
	}
	s.presentation().operators[key] = text
	return text, nil
}
func (s *StatementScope) FormatRegoper(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoper)
}
func (s *StatementScope) FormatRegoperator(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoperator)
}
