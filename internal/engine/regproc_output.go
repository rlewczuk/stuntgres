package engine

import "stuntgres/internal/engine/exec"

func (s *StatementScope) FormatRegproc(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegproc(id)
	}
	return s.presentation().spellings.Regproc(s.inquiry.commentCatalog, s.inquiry.deparsePath, id), nil
}
func (s *StatementScope) FormatRegprocedure(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegprocedure(id)
	}
	return s.presentation().spellings.Regprocedure(s.inquiry.commentCatalog, s.inquiry.deparsePath, id), nil
}
