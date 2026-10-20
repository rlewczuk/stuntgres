package engine

import (
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
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || relation.Sequence == nil || !relation.Sequence.Identity {
		return sqlError("0A000", "restore accepts only generated sequence ownership or identity configuration")
	}
	if syntax.Action == "logged" || syntax.Action == "unlogged" {
		return nil
	}
	if syntax.Action != "" || len(syntax.Options) == 0 {
		return sqlError("0A000", "restore accepts only generated identity configuration")
	}
	for _, option := range syntax.Options {
		switch option.Name {
		case "as", "increment", "min", "max", "start", "cache", "cycle", "restart":
		default:
			return sqlError("0A000", "restore accepts only generated identity configuration")
		}
	}
	return nil
}
