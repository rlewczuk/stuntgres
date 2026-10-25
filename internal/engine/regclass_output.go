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
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	path, err := ReferencePath(auth)
	if err != nil {
		return err
	}
	collationPath := func() ([]ids.ObjectAddress, error) { return LookupPath(auth, analyze.CollationLookup) }
	return regobj.CaptureRowsOutput(rows, auth.Catalog, &e.Catalog.Owner, auth.Roles, path, collationPath)
}

// String output uses the same captured namespace presentation as exec.FinalRows,
// without changing the canonical OID datum or consulting a later live catalog.
func (s *StatementScope) FormatRegclass(id uint32) (string, error) {
	if s == nil || s.Inquiry.CommentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegclass(id)
	}
	return regobj.ClassSpelling(s.Inquiry.CommentCatalog, s.Inquiry.DeparsePath, ids.OID(id)), nil
}
