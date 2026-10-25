package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func preparePhysicalNotNullConstructor(e *Engine, op *session.Operation, raw *parser.AlterTable, repairs []table.PhysicalNotNullRepair, created map[ids.ObjectAddress]ids.ObjectRef) (*table.PhysicalNotNullConstructor, error) {
	if len(repairs) == 0 {
		return nil, nil
	}
	if !table.PhysicalNotNullConstructorShape(raw, repairs) {
		return nil, SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !PartitionConstraintReplayImport(e, op, auth) {
		return nil, SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	return table.PreparePhysicalNotNullConstructor(auth.Catalog, ref, raw, repairs, created)
}

// Capture actual normal constructor results, not a generated-name prediction.
func capturePhysicalNotNullConstructor(e *Engine, op *session.Operation, constructor *table.PhysicalNotNullConstructor, indexes map[ids.ObjectAddress]*catalog.Relation) (*table.RestoredPhysicalNotNull, error) {
	auth, err := OperationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !PartitionConstraintReplayImport(e, op, auth) || constructor.Ref.Epoch != auth.Catalog.Epoch() {
		return nil, SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return table.CapturePhysicalNotNullConstructor(op, auth.RoleRecord, auth.Catalog, constructor, indexes)
}

// The selected NN and its outbound dependency projection disappear together in
// the sole private stage. The surviving physical flag is not a synthetic NN row.
func restorePhysicalNotNull(e *Engine, op *session.Operation, raw *parser.Delete, pair *table.RestoredPhysicalNotNull) error {
	repair, valid := table.RestorePhysicalNotNullSyntax(raw)
	reject := func() error { return SQLError("0A000", table.RestorePhysicalNotNullBoundary) }
	if !valid || !pair.Pairs(op, repair) {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return SQLError("42501", "permission denied for table pg_constraint")
	}
	e.Mu.Lock()
	if !PartitionConstraintReplayImport(e, op, auth) || auth.Catalog.Epoch() != pair.Ref.Epoch || !pair.Holds(auth.RoleRecord, auth.Catalog) {
		e.Mu.Unlock()
		return reject()
	}
	ref, err := e.ResolveRelationReference(auth, repair.Relation, false)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	next, err := table.PhysicalNotNullCleanup(auth.Catalog, pair, ref, repair)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := publishRestoreRepairLocked(e, op, auth, next, ref.Epoch); err != nil {
		return err
	}
	pair.Advance(next)
	return nil
}
