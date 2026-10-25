package engine

import (
	"fmt"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func pairRestoredNotNull(e *core.Engine, op *session.Operation, raw *parser.AlterTable, origins map[ids.OID]table.RestoredNotNull) (table.RestoredNotNull, error) {
	if !table.RestoredNotNullShape(raw) {
		return table.RestoredNotNull{}, core.SQLError("0A000", table.RestoreNoInheritBoundary)
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return table.RestoredNotNull{}, err
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredNotNull{}, err
	}
	return table.PairRestoredNotNull(auth.Catalog, ref, raw, origins)
}

// Unlike public UPDATE, this interpreter can only run inside isolated Restore,
// after an imported explicit validated local NN and its immediately paired SET.
func restoreNoInheritNotNull(e *core.Engine, op *session.Operation, raw *parser.Update, origin table.RestoredNotNull) error {
	name, column, label, err := table.RestoreNoInheritSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return core.SQLError("42501", "permission denied for table pg_constraint")
	}
	e.Mu.Lock()
	if !core.PartitionConstraintReplayImport(e, op, auth) {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore no-inherit authority")
	}
	ref, err := e.ResolveRelationReference(auth, name, false)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	next, err := table.NoInheritNotNullCandidate(auth.Catalog, ref, origin, column, label)
	if err != nil || next == nil {
		e.Mu.Unlock()
		return err
	}
	return publishRestoreRepairLocked(e, op, auth, next, ref.Epoch)
}
