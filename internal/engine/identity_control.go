package engine

import (
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Restore admits only the parameter-free configuration of an existing internal
// sequence. It is reconstruction in the isolated engine, not ALTER history.
func validateRestoreIdentityConfiguration(e *Engine, op *session.Operation, syntax *parser.AlterSequence) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.ResolveRelationReference(auth, syntax.Name, false)
	if err != nil {
		return err
	}
	relation, _ := auth.Catalog.Relation(ref.Address)
	return sequence.CheckRestoreIdentityConfiguration(relation, syntax)
}
