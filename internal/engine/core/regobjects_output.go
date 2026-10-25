package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

func (s *StatementScope) FormatRegcollation(id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegcollation(id)
	}
	return regobj.ObjectSpelling(s.Inquiry.CommentCatalog, s.Inquiry.collationPath, ids.CollationClassID, ids.OID(id)), nil
}
func (s *StatementScope) FormatRegnamespace(id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegnamespace(id)
	}
	return regobj.ObjectSpelling(s.Inquiry.CommentCatalog, nil, ids.NamespaceClassID, ids.OID(id)), nil
}
