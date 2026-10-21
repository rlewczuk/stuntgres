package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func (s *StatementScope) FormatRegproc(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegproc(id)
	}
	if text, ok := s.presentationOrZero().regproc[id]; ok {
		return text, nil
	}
	text := ProcSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id))
	if s.presentationOrZero().regproc == nil {
		s.presentation().regproc = make(map[uint32]string)
	}
	s.presentation().regproc[id] = text
	return text, nil
}
func (s *StatementScope) FormatRegprocedure(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegprocedure(id)
	}
	if text, ok := s.presentationOrZero().regprocedure[id]; ok {
		return text, nil
	}
	text := RoutineSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id), true, false)
	if s.presentationOrZero().regprocedure == nil {
		s.presentation().regprocedure = make(map[uint32]string)
	}
	s.presentation().regprocedure[id] = text
	return text, nil
}
