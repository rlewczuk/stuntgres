package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) FormatRegcollation(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegcollation(id)
	}
	return regobj.ObjectSpelling(s.inquiry.commentCatalog, s.inquiry.collationPath, ids.CollationClassID, ids.OID(id)), nil
}
func (s *StatementScope) FormatRegnamespace(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegnamespace(id)
	}
	return regobj.ObjectSpelling(s.inquiry.commentCatalog, nil, ids.NamespaceClassID, ids.OID(id)), nil
}

func (e *Engine) captureRegObjectRowsValueLocked(rows *exec.FinalRows, auth AuthorizationView, path *[]ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().ContainsRegcollation() && *path == nil {
		var err error
		*path, err = e.lookupPath(auth, analyze.CollationLookup)
		if err != nil {
			return err
		}
	}
	return regobj.CaptureObjectOutputValue(rows, auth.Catalog, *path, value)
}
