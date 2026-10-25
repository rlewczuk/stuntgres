package engine

import "stuntgres/internal/engine/exec"

func (s *StatementScope) FormatRegproc(id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegproc(id)
	}
	return s.presentation().spellings.Regproc(s.Inquiry.CommentCatalog, s.Inquiry.DeparsePath, id), nil
}
func (s *StatementScope) FormatRegprocedure(id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegprocedure(id)
	}
	return s.presentation().spellings.Regprocedure(s.Inquiry.CommentCatalog, s.Inquiry.DeparsePath, id), nil
}
