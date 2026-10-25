package engine

import (
	"context"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Restore hook entries owned by tables: physical NOT NULL constructors and
// their foreign key providers, NOT NULL pairing, foreign keys and deferred
// defaults. Each calls the existing Engine method on the restore stage.

// beforeRestorePhysicalBoundary rejects a statement that interrupts a
// physical NOT NULL repair sequence.
func beforeRestorePhysicalBoundary(r *restoreRun, _ *session.Operation, stmt parser.Statement) (bool, error) {
	if r.pairedPhysical != nil && !r.pairedPhysical.Admits(stmt) {
		return false, core.SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return false, nil
}

// beforeRestorePhysicalConstructor recognizes an ALTER TABLE that builds a
// physical NOT NULL constructor, or the deferred primary key that completes
// one.
func beforeRestorePhysicalConstructor(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok {
		return false, nil
	}
	var err error
	r.cur.physicalConstructor, err = preparePhysicalNotNullConstructor(r.stage, op, alteration, table.PhysicalNotNullFollowing(r.statements, r.cur.index), r.historicalRefs)
	if err != nil || r.cur.physicalConstructor != nil {
		return false, err
	}
	r.cur.physicalConstructor, err = prepareDeferredPhysicalPrimary(r.stage, op, alteration, r.deferredPhysical, r.historicalRefs)
	return false, err
}

// beforeRestoreDeferredPhysicalOwner keeps a deferred physical primary key
// across one owner directive and drops it at any other statement.
func beforeRestoreDeferredPhysicalOwner(r *restoreRun, _ *session.Operation, stmt parser.Statement) (bool, error) {
	if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && r.deferredPhysical != nil && !r.deferredPhysical.OwnerDirective {
		r.deferredPhysical.OwnerDirective = true
	} else {
		r.deferredPhysical = nil
	}
	return false, nil
}

// beforeRestorePhysicalIndexBase keeps the catalog a CREATE INDEX starts
// from, to find the index it creates.
func beforeRestorePhysicalIndexBase(r *restoreRun, _ *session.Operation, stmt parser.Statement) (bool, error) {
	if _, ok := stmt.(*parser.CreateIndex); ok {
		r.cur.physicalIndexBase = r.stage.CatalogRead()
	}
	return false, nil
}

// beforeRestorePhysicalSelfForeignKey follows a physical foreign key
// provider: it keeps it across one owner directive, captures a
// self-referencing foreign key (consuming the statement), or prepares a
// competing unique constraint. Any other statement drops the provider.
func beforeRestorePhysicalSelfForeignKey(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	if r.physicalForeignProvider == nil {
		return false, nil
	}
	if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && !r.physicalForeignProvider.OwnerDirective {
		r.physicalForeignProvider.OwnerDirective = true
		return false, nil
	}
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok {
		r.physicalForeignProvider = nil
		return false, nil
	}
	declaration, admitted, err := capturePhysicalSelfForeignKey(r.stage, op, alteration, r.physicalForeignProvider, r.historicalRefs)
	if err != nil {
		return false, err
	}
	if admitted {
		r.pendingForeignKeys = append(r.pendingForeignKeys, declaration)
		r.pairedNotNull = table.RestoredNotNull{}
		return true, nil
	}
	r.cur.physicalConstructor, err = preparePhysicalCompetingUnique(r.stage, op, alteration, r.physicalForeignProvider, r.historicalRefs)
	if err != nil {
		return false, err
	}
	if r.cur.physicalConstructor == nil {
		r.physicalForeignProvider = nil
	}
	return false, nil
}

// prepareRestoreNotNullPairReset ends a NOT NULL pair at any statement
// other than an owner directive.
func prepareRestoreNotNullPairReset(r *restoreRun, _ *session.Operation, stmt parser.Statement) error {
	if _, ownerDirective := stmt.(*parser.SessionAuthorization); !ownerDirective {
		r.pairedNotNull = table.RestoredNotNull{}
	}
	return nil
}

// prepareRestoreNotNullPair pairs a SET NOT NULL with its restored origin.
func prepareRestoreNotNullPair(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok || r.cur.physicalConstructor != nil || len(alteration.Actions) != 1 || alteration.Actions[0].Action != parser.SetNotNull {
		return nil
	}
	var err error
	r.pairedNotNull, err = pairRestoredNotNull(r.stage, op, alteration, r.notNullOrigins)
	return err
}

// afterRestorePhysicalConstructor captures the physical NOT NULL repairs
// the constructor's statement started.
func afterRestorePhysicalConstructor(r *restoreRun, op *session.Operation, _ parser.Statement, _ control.Outcome) error {
	if r.cur.physicalConstructor == nil {
		return nil
	}
	var err error
	r.pairedPhysical, err = capturePhysicalNotNullConstructor(r.stage, op, r.cur.physicalConstructor, r.physicalIndexes)
	return err
}

// afterRestorePhysicalForeignKeyProvider captures the unique constraint a
// following physical self-referencing foreign key may use.
func afterRestorePhysicalForeignKeyProvider(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok || len(alteration.Actions) != 1 || r.pairedPhysical != nil || (alteration.Actions[0].Action != parser.AddUnique && alteration.Actions[0].Action != parser.AddUsingIndex) {
		return nil
	}
	var err error
	r.physicalForeignProvider, err = capturePhysicalForeignKeyProvider(r.stage, op, alteration.Name, r.historicalRefs)
	if err != nil {
		return err
	}
	if r.physicalForeignProvider != nil && r.cur.physicalConstructor != nil && r.cur.physicalConstructor.Competing {
		r.physicalForeignProvider.Permuted = true
	}
	return nil
}

// afterRestorePhysicalImportIndex records the index a CREATE INDEX made.
func afterRestorePhysicalImportIndex(r *restoreRun, _ *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	creation, ok := stmt.(*parser.CreateIndex)
	if !ok {
		return nil
	}
	if index := table.CapturePhysicalImportIndex(r.cur.physicalIndexBase, r.stage.CatalogRead(), r.stage.Catalog.Domain, creation, r.historicalRefs); index != nil {
		r.physicalIndexes[index.Address()] = index
	}
	return nil
}

// afterRestoreNotNullOrigins records the NOT NULL constraints a CREATE TABLE
// made, for pairing with later repairs.
func afterRestoreNotNullOrigins(r *restoreRun, _ *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	if creation, ok := stmt.(*parser.CreateTable); ok && outcome.Created != (ids.ObjectRef{}) {
		table.CaptureRestoredNotNulls(r.stage.CatalogRead(), outcome.Created, creation, r.notNullOrigins)
	}
	return nil
}

// afterRestoreForeignKeys resolves the foreign keys admission stripped from
// a CREATE TABLE; finish binds them.
func afterRestoreForeignKeys(r *restoreRun, _ *session.Operation, _ parser.Statement, outcome control.Outcome) error {
	if outcome.Created == (ids.ObjectRef{}) || len(r.cur.pending.Definitions) == 0 {
		return nil
	}
	r.cur.pending.Child = outcome.Created
	if err := resolveRestoredForeignKeyTargets(r.stage, &r.cur.pending); err != nil {
		return err
	}
	r.pendingForeignKeys = append(r.pendingForeignKeys, r.cur.pending)
	return nil
}

// afterRestoreDefault validates a deferred DEFAULT alteration.
func afterRestoreDefault(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	if alteration, ok := stmt.(*parser.AlterTableDefault); ok {
		return validateRestoredDefault(r.stage, op, alteration)
	}
	if foreign, ok := stmt.(*parser.ForeignDDL); ok {
		if alteration := restoreForeignDefault(foreign); alteration != nil {
			qualified := *alteration
			qualified.Name = foreign.Name
			return validateRestoredDefault(r.stage, op, &qualified)
		}
	}
	return nil
}

// finishRestorePhysicalBoundary rejects a script that ends inside a
// physical NOT NULL repair sequence.
func finishRestorePhysicalBoundary(r *restoreRun, _ context.Context, _ *session.Operation) error {
	if r.pairedPhysical != nil {
		return core.SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return nil
}

// finishRestoreForeignKeys binds every captured foreign key.
func finishRestoreForeignKeys(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return bindRestoredForeignKeys(r.stage, ctx, op, r.pendingForeignKeys)
}
