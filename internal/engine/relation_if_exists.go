package engine

import "stuntgres/internal/engine/session"

// skipMissingRelationLocked completes ALTER ... IF EXISTS on a relation that is
// not found: it publishes the operation, marks the scope completed and emits
// the skip NOTICE. Engine.mu is held on entry and released on every return.
// The partition forms unlock before checking the publication error and do not
// check the context.
func skipMissingRelationLocked(e *Engine, scope *StatementScope, op *session.Operation, name string, partition bool) (Result, error) {
	op.Session.Mu.Lock()
	err := PublicationOperationLocked(e, op)
	op.Session.Mu.Unlock()
	if partition {
		e.Mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.Completed = true
	} else {
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		scope.Completed = true
		e.Mu.Unlock()
	}
	op.EmitNotice(session.LevelNotice, session.MissingRelation(name))
	return Result{}, nil
}
