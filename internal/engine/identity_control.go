package engine

import (
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Restore admits only the parameter-free configuration of an existing internal
// sequence. It is reconstruction in the isolated engine, not ALTER history.
func (e *Engine) validateRestoreIdentityConfiguration(op *session.Operation, syntax *parser.AlterSequence) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.resolveRelationReference(auth, syntax.Name, false)
	if err != nil {
		return err
	}
	relation, _ := auth.Catalog.Relation(ref.Address)
	return sequence.CheckRestoreIdentityConfiguration(relation, syntax)
}
