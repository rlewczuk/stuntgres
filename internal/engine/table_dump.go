package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Replay uses the ordinary binder/publication path, then admits the same
// validated captured-reference and selected regclass definitions Dump defers.
func (e *Engine) validateRestoredDefault(op *session.Operation, s *parser.AlterTableDefault) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.resolveReference(auth, s.Name)
	if err != nil {
		return err
	}
	return ValidateRestoredDefault(auth.Catalog, ref, s)
}
