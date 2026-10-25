package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func restoreRetiredConstraint(e *core.Engine, op *session.Operation, raw *parser.Update, authority *table.RestoredHistoryAuthority) error {
	repair, err := table.RestoreRetiredSyntax(raw)
	if err != nil {
		return err
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error { return core.SQLError("0A000", table.RestoreRetiredBoundary) }
	if !authority.Complete() {
		return reject()
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	if !core.PartitionConstraintReplayImport(e, op, auth) {
		e.Mu.Unlock()
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil || !actor.Superuser {
		e.Mu.Unlock()
		return reject()
	}
	ref, err := e.ResolveRelationReference(auth, repair.Relation, false)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	next, commit, err := table.RetiredConstraintCandidate(TableHost{core.DDLHost{E: e, Op: op}}, auth, auth.Catalog, ref, repair, authority)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := publishRestoreRepairLocked(e, op, auth, next, ref.Epoch); err != nil {
		return err
	}
	authority.CommitRetiredRepair(commit)
	return nil
}
