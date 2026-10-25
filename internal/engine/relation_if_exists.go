package engine

import "stuntgres/internal/engine/core"

import "stuntgres/internal/engine/session"

// SkipMissingRelationLocked completes ALTER ... IF EXISTS on a relation that is
// not found: it publishes the operation, marks the scope completed and emits
// the skip NOTICE. Engine.mu is held on entry and released on every return.
// The partition forms unlock before checking the publication error and do not
// check the context.
func SkipMissingRelationLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, name string, partition bool) (core.Result, error) {
	op.Session.Mu.Lock()
	err := core.PublicationOperationLocked(e, op)
	op.Session.Mu.Unlock()
	if partition {
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		scope.Completed = true
	} else {
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		scope.Completed = true
		e.Mu.Unlock()
	}
	op.EmitNotice(session.LevelNotice, session.MissingRelation(name))
	return core.Result{}, nil
}
