package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) restoreRetiredConstraint(op *session.Operation, raw *parser.Update, authority *table.RestoredHistoryAuthority) error {
	repair, err := table.RestoreRetiredSyntax(raw)
	if err != nil {
		return err
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return sqlError("0A000", table.RestoreRetiredBoundary) }
	if !authority.Complete() {
		return reject()
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) {
		e.mu.Unlock()
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil || !actor.Superuser {
		e.mu.Unlock()
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, repair.Relation, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	next, commit, err := table.RetiredConstraintCandidate(ddlHost{e: e, op: op}, auth, auth.Catalog, ref, repair, authority)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if err := e.publishRestoreRepairLocked(op, auth, next, ref.Epoch); err != nil {
		return err
	}
	authority.CommitRetiredRepair(commit)
	return nil
}
