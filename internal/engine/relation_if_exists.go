package engine

import "stuntgres/internal/engine/session"

// skipMissingRelationLocked completes ALTER ... IF EXISTS on a relation that is
// not found: it publishes the operation, marks the scope completed and emits
// the skip NOTICE. Engine.mu is held on entry and released on every return.
// The partition forms unlock before checking the publication error and do not
// check the context.
func (e *Engine) skipMissingRelationLocked(scope *StatementScope, op *session.Operation, name string, partition bool) (Result, error) {
	op.Session.Mu.Lock()
	err := publicationOperationLocked(e, op)
	op.Session.Mu.Unlock()
	if partition {
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
	} else {
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		scope.completed = true
		e.mu.Unlock()
	}
	op.EmitNotice(session.LevelNotice, session.MissingRelation(name))
	return Result{}, nil
}
