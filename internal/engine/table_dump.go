package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Replay uses the ordinary binder/publication path, then admits the same
// validated captured-reference and selected regclass definitions Dump defers.
func (e *Engine) validateRestoredDefault(op *session.Operation, s *parser.AlterTableDefault) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.ResolveReference(auth, s.Name)
	if err != nil {
		return err
	}
	return table.ValidateRestoredDefault(auth.Catalog, ref, s)
}
