package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// StagePrivateColumnShape calls stagePrivateColumnShape.
func StagePrivateColumnShape(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, old *catalog.Relation, next *catalog.Relation, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, snapshot visibility.Root, snapshotExists bool, stable bool) ([]storage.RowVersion, *visibility.PrivateDeletes, bool, error) {
	return stagePrivateColumnShape(evaluation, stage, changes, old, next, writes, deletes, snapshot, snapshotExists, stable)
}

// EnginePrepareQueryTableLocked calls x.prepareQueryTableLocked.
func EnginePrepareQueryTableLocked(x *Engine, op *session.Operation, auth AuthorizationView, s *parser.CreateQueryTable, b analyze.Binding) (*CatalogStage, *catalog.Relation, bool, error) {
	return x.prepareQueryTableLocked(op, auth, s, b)
}

// ScopeCheckExclusionCommandEvents calls x.checkExclusionCommandEvents.
func ScopeCheckExclusionCommandEvents(x *StatementScope, all bool, selected map[ids.OID]bool, bounded bool) error {
	return x.checkExclusionCommandEvents(all, selected, bounded)
}

// ScopeCheckExclusionEvents calls x.checkExclusionEvents.
func ScopeCheckExclusionEvents(x *StatementScope, all bool, selected map[ids.OID]bool) error {
	return x.checkExclusionEvents(all, selected)
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
