package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

func (s *StatementScope) FormatRegtype(id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegtype(id)
	}
	return regobj.TypeSpelling(s.Inquiry.CommentCatalog, s.Inquiry.DeparsePath, ids.OID(id)), nil
}
