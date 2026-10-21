package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) pairRestoredNotNull(op *session.Operation, raw *parser.AlterTable, origins map[ids.OID]table.RestoredNotNull) (table.RestoredNotNull, error) {
	if !table.RestoredNotNullShape(raw) {
		return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return table.RestoredNotNull{}, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredNotNull{}, err
	}
	return table.PairRestoredNotNull(auth.Catalog, ref, raw, origins)
}

// Unlike public UPDATE, this interpreter can only run inside isolated Restore,
// after an imported explicit validated local NN and its immediately paired SET.
func (e *Engine) restoreNoInheritNotNull(op *session.Operation, raw *parser.Update, origin table.RestoredNotNull) error {
	name, column, label, err := table.RestoreNoInheritSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_constraint")
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore no-inherit authority")
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	next, err := table.NoInheritNotNullCandidate(auth.Catalog, ref, origin, column, label)
	if err != nil || next == nil {
		e.mu.Unlock()
		return err
	}
	return e.publishRestoreRepairLocked(op, auth, next, ref.Epoch)
}
