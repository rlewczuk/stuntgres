package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Only the actual completed constructor/cleanup can issue this short-lived
// readiness receipt. It never creates a key or grants foreign-key privileges.
func (e *Engine) capturePhysicalForeignKeyProvider(op *session.Operation, name parser.ObjectName, created map[ids.ObjectAddress]ids.ObjectRef) (*table.RestoredPhysicalForeignKeyProvider, error) {
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		return nil, err
	}
	return table.CapturePhysicalForeignKeyProvider(op, auth.RoleRecord, auth.Catalog, ref, created)
}

// Reuse the incumbent captured-FK binder, including its one data validation and
// publication. The optional supporting slot pins only this genuine current PK;
// ordinary declarations continue to use the unchanged zero-slot selection.
func (e *Engine) capturePhysicalSelfForeignKey(op *session.Operation, raw *parser.AlterTable, provider *table.RestoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (table.RestoredForeignKeys, bool, error) {
	if handled, err := table.PhysicalSelfForeignKeyShape(op, raw, provider); !handled || err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	e.mu.Lock()
	valid := partitionConstraintReplayImport(e, op, auth) && provider.Holds(auth.RoleRecord, auth.Catalog) && created[provider.Ref.Address] == provider.Ref && provider.Ref.Epoch == auth.Catalog.Epoch()
	e.mu.Unlock()
	if !valid {
		return table.RestoredForeignKeys{}, false, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	if err := table.PhysicalSelfForeignKeyColumns(auth.Catalog, raw, provider); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	pending, err := e.captureRestoredForeignKeys(op, []parser.ForeignKeyConstraint{*raw.Actions[0].ForeignKey})
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	pending.Child = provider.Ref
	if err := e.resolveRestoredForeignKeyTargets(&pending); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	if err := table.PinPhysicalSelfForeignKey(&pending, provider); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	return pending, true, nil
}
