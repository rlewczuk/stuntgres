package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Only the actual completed constructor/cleanup can issue this short-lived
// readiness receipt. It never creates a key or grants foreign-key privileges.
func CapturePhysicalForeignKeyProvider(e *core.Engine, op *session.Operation, name parser.ObjectName, created map[ids.ObjectAddress]ids.ObjectRef) (*table.RestoredPhysicalForeignKeyProvider, error) {
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !core.PartitionConstraintReplayImport(e, op, auth) {
		return nil, core.SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	ref, err := e.ResolveRelationReference(auth, name, false)
	if err != nil {
		return nil, err
	}
	return table.CapturePhysicalForeignKeyProvider(op, auth.RoleRecord, auth.Catalog, ref, created)
}

// Reuse the incumbent captured-FK binder, including its one data validation and
// publication. The optional supporting slot pins only this genuine current PK;
// ordinary declarations continue to use the unchanged zero-slot selection.
func CapturePhysicalSelfForeignKey(e *core.Engine, op *session.Operation, raw *parser.AlterTable, provider *table.RestoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (table.RestoredForeignKeys, bool, error) {
	if handled, err := table.PhysicalSelfForeignKeyShape(op, raw, provider); !handled || err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	e.Mu.Lock()
	valid := core.PartitionConstraintReplayImport(e, op, auth) && provider.Holds(auth.RoleRecord, auth.Catalog) && created[provider.Ref.Address] == provider.Ref && provider.Ref.Epoch == auth.Catalog.Epoch()
	e.Mu.Unlock()
	if !valid {
		return table.RestoredForeignKeys{}, false, core.SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	if err := table.PhysicalSelfForeignKeyColumns(auth.Catalog, raw, provider); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	pending, err := CaptureRestoredForeignKeys(e, op, []parser.ForeignKeyConstraint{*raw.Actions[0].ForeignKey})
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	pending.Child = provider.Ref
	if err := ResolveRestoredForeignKeyTargets(e, &pending); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	if err := table.PinPhysicalSelfForeignKey(&pending, provider); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	return pending, true, nil
}
