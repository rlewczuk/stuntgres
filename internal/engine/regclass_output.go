package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
)

// Output presentation is captured once before returning Rows. The internal
// datum stays an OID, and retained Rows never consult later catalog authority.
func (e *Engine) captureRegclassOutput(op *session.Operation, rows *exec.FinalRows) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	path, err := referencePath(auth)
	if err != nil {
		return err
	}
	collationPath := func() ([]ids.ObjectAddress, error) { return lookupPath(auth, analyze.CollationLookup) }
	return regobj.CaptureRowsOutput(rows, auth.Catalog, e.catalog.owner(), auth.Roles, path, collationPath)
}

// String output uses the same captured namespace presentation as exec.FinalRows,
// without changing the canonical OID datum or consulting a later live catalog.
func (s *StatementScope) FormatRegclass(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegclass(id)
	}
	return regobj.ClassSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id)), nil
}
