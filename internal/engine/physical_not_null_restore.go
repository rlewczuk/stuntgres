package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) preparePhysicalNotNullConstructor(op *session.Operation, raw *parser.AlterTable, repairs []table.PhysicalNotNullRepair, created map[ids.ObjectAddress]ids.ObjectRef) (*table.PhysicalNotNullConstructor, error) {
	if len(repairs) == 0 {
		return nil, nil
	}
	if !PhysicalNotNullConstructorShape(raw, repairs) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	return PreparePhysicalNotNullConstructor(auth.Catalog, ref, raw, repairs, created)
}

// Capture actual normal constructor results, not a generated-name prediction.
func (e *Engine) capturePhysicalNotNullConstructor(op *session.Operation, constructor *table.PhysicalNotNullConstructor, indexes map[ids.ObjectAddress]*catalog.Relation) (*RestoredPhysicalNotNull, error) {
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || constructor.Ref.Epoch != auth.Catalog.Epoch() {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return CapturePhysicalNotNullConstructor(op, auth.RoleRecord, auth.Catalog, constructor, indexes)
}

// The selected NN and its outbound dependency projection disappear together in
// the sole private stage. The surviving physical flag is not a synthetic NN row.
func (e *Engine) restorePhysicalNotNull(op *session.Operation, raw *parser.Delete, pair *RestoredPhysicalNotNull) error {
	repair, valid := table.RestorePhysicalNotNullSyntax(raw)
	reject := func() error { return sqlError("0A000", table.RestorePhysicalNotNullBoundary) }
	if !valid || !pair.Pairs(op, repair) {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
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
	if !partitionConstraintReplayImport(e, op, auth) || auth.Catalog.Epoch() != pair.Ref.Epoch || !pair.Holds(auth.RoleRecord, auth.Catalog) {
		e.mu.Unlock()
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, repair.Relation, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	next, err := PhysicalNotNullCleanup(auth.Catalog, pair, ref, repair)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if err := e.publishRestoreRepairLocked(op, auth, next, ref.Epoch); err != nil {
		return err
	}
	pair.Advance(next)
	return nil
}
