package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
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
	path, err := e.referencePath(auth)
	if err != nil {
		return err
	}
	var collationPath []ids.ObjectAddress
	aclOutput := capturedACLItemOutput{Owner: e.catalog.owner(), Roles: auth.Roles}
	roleOutput := capturedRegroleOutput{Owner: e.catalog.owner(), Roles: auth.Roles, QuoteAll: rows.Output.QuoteAllIdentifiers}
	for _, value := range rows.Values {
		if err := e.captureRegObjectRowsValueLocked(rows, auth, &collationPath, value); err != nil {
			return err
		}
		if err := CaptureRoleOutputValue(rows, roleOutput, value); err != nil {
			return err
		}
		if err := CaptureACLItemOutputValue(rows, aclOutput, value); err != nil {
			return err
		}
		if err := CaptureClassOutputValue(rows, auth.Catalog, path, value); err != nil {
			return err
		}
	}
	for _, row := range rows.Anonymous {
		for _, value := range row {
			if err := e.captureRegObjectRowsValueLocked(rows, auth, &collationPath, value); err != nil {
				return err
			}
			if err := CaptureRoleOutputValue(rows, roleOutput, value); err != nil {
				return err
			}
			if err := CaptureACLItemOutputValue(rows, aclOutput, value); err != nil {
				return err
			}
			if err := CaptureClassOutputValue(rows, auth.Catalog, path, value); err != nil {
				return err
			}
		}
	}
	if rows.Source != nil {
		for i := 0; i < rows.Count; i++ {
			index := i
			if rows.Selection != nil {
				index = rows.Selection[i]
			}
			row := rows.Source.Row(index)
			for _, slot := range rows.Slots {
				if err := e.captureRegObjectRowsValueLocked(rows, auth, &collationPath, row[slot]); err != nil {
					return err
				}
				if err := CaptureRoleOutputValue(rows, roleOutput, row[slot]); err != nil {
					return err
				}
				if err := CaptureACLItemOutputValue(rows, aclOutput, row[slot]); err != nil {
					return err
				}
				if err := CaptureClassOutputValue(rows, auth.Catalog, path, row[slot]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// String output uses the same captured namespace presentation as exec.FinalRows,
// without changing the canonical OID datum or consulting a later live catalog.
func (s *StatementScope) FormatRegclass(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegclass(id)
	}
	return ClassSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id)), nil
}
