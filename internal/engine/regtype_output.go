package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func (s *StatementScope) FormatRegtype(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegtype(id)
	}
	return TypeSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id)), nil
}
