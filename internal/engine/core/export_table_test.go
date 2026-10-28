package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// StagePrivateColumnShape calls stagePrivateColumnShape.
func StagePrivateColumnShape(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, old *catalog.Relation, next *catalog.Relation, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, snapshot visibility.Root, snapshotExists bool, stable bool) ([]storage.RowVersion, *visibility.PrivateDeletes, bool, error) {
	return stagePrivateColumnShape(evaluation, stage, changes, old, next, writes, deletes, snapshot, snapshotExists, stable)
}

// ScopeCheckConstraintCommandEvents calls x.checkConstraintCommandEvents.
func ScopeCheckConstraintCommandEvents(x *StatementScope, all bool, selected map[ids.OID]bool, bounded bool) error {
	return x.checkConstraintCommandEvents(all, selected, bounded)
}

// ScopeCheckConstraintEvents calls x.checkConstraintEvents.
func ScopeCheckConstraintEvents(x *StatementScope, all bool, selected map[ids.OID]bool) error {
	return x.checkConstraintEvents(all, selected)
}

// ScopeForeignActions calls x.foreignActions.
func ScopeForeignActions(x *StatementScope) *dml.ForeignActionScope {
	return x.foreignActions()
}

// ScopeForeignActionsOrZero calls x.foreignActionsOrZero.
func ScopeForeignActionsOrZero(x *StatementScope) dml.ForeignActionScope {
	return x.foreignActionsOrZero()
}

// ScopeForeignEffectRelevant calls x.foreignEffectRelevant.
func ScopeForeignEffectRelevant(x *StatementScope, ref ids.ObjectRef, view catalog.Read) bool {
	return x.foreignEffectRelevant(ref, view)
}

// ForeignEffectAdmissionCatalog returns x.catalog.
func ForeignEffectAdmissionCatalog(x *foreignEffectAdmission) *catalog.State { return x.catalog }

// ForeignEffectAdmissionRef returns x.ref.
func ForeignEffectAdmissionRef(x *foreignEffectAdmission) ids.ObjectRef { return x.ref }

// ForeignEffectAdmissionFields holds the foreignEffectAdmission fields tests set.
type ForeignEffectAdmissionFields struct {
}

// NewForeignEffectAdmission builds a foreignEffectAdmission from f.
func NewForeignEffectAdmission(f ForeignEffectAdmissionFields) foreignEffectAdmission {
	return foreignEffectAdmission{}
}

// EnginePublishQueryTable calls x.publishQueryTable.
func EnginePublishQueryTable(x *Engine, scope *StatementScope, stage *CatalogStage) error {
	return x.publishQueryTable(scope, stage)
}

// EngineRepairRemappedGenerationsLocked calls x.repairRemappedGenerationsLocked.
func EngineRepairRemappedGenerationsLocked(x *Engine, ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, mapping map[ids.ObjectAddress]ids.ObjectAddress, op *session.Operation, indexes *index.Snapshot) (*Engine, error) {
	return x.repairRemappedGenerationsLocked(ctx, next, relations, sequences, mapping, op, indexes)
}

// ScopeAdmitForeignAction calls x.admitForeignAction.
func ScopeAdmitForeignAction(x *StatementScope, ref ids.ObjectRef, job *dml.ForeignKeyJob, writable bool) error {
	return x.admitForeignAction(ref, job, writable)
}

// ScopeValidateForeignActionLocked calls x.validateForeignActionLocked.
func ScopeValidateForeignActionLocked(x *StatementScope, e *Engine, ref ids.ObjectRef) error {
	return x.validateForeignActionLocked(e, ref)
}

// ScopeWriteAuthorizationLocked calls x.writeAuthorizationLocked.
func ScopeWriteAuthorizationLocked(x *StatementScope, e *Engine) (AuthorizationView, error) {
	return x.writeAuthorizationLocked(e)
}

// WriteStageDeleteForeignAction calls x.deleteForeignAction.
func WriteStageDeleteForeignAction(x *WriteStage, ref ids.ObjectRef, old storage.RowVersion) error {
	return x.deleteForeignAction(ref, old)
}
