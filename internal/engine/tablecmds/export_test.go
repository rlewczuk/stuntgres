package tablecmds

import (
	"stuntgres/internal/engine/table"
)

// Exported names of tablecmds internals for the external tests in this
// directory.

var (
	AdmitInheritanceRelationship      = admitInheritanceRelationship
	AlterInheritanceDefaultLocked     = alterInheritanceDefaultLocked
	AlterInheritanceLocked            = alterInheritanceLocked
	BindCreateDefaults                = bindCreateDefaults
	BindCreateGenerations             = bindCreateGenerations
	BindIndexExpression               = bindIndexExpression
	BindIndexPredicate                = bindIndexPredicate
	DropDiscoveryInput                = dropDiscoveryInput
	ForeignKeysOrZero                 = foreignKeysOrZero
	HoldsColumnDropGrant              = holdsColumnDropGrant
	IdentityCandidate                 = identityCandidate
	InheritancePlan                   = inheritancePlan
	InheritedAlterActions             = inheritedAlterActions
	PrepareAlterTableActions          = prepareAlterTableActions
	PrepareColumnDrop                 = prepareColumnDrop
	PreparedDropOf                    = preparedDropOf
	RenameInheritanceColumnLocked     = renameInheritanceColumnLocked
	RenameInheritanceConstraintLocked = renameInheritanceConstraintLocked
	RetiredBase                       = retiredBase
	ScopeDropsOrZero                  = scopeDropsOrZero
	StageAddedUnique                  = stageAddedUnique
	StageAlterIdentityTypes           = stageAlterIdentityTypes
	StageDroppedConstraintIndexes     = stageDroppedConstraintIndexes
	StagePartitionConstraintDrop      = stagePartitionConstraintDrop
	StageRowSecurityFlagsLocked       = stageRowSecurityFlagsLocked
	TableStage                        = tableStage
)

// NewPreparedIdentityDrop is a prepared identity DROP that projects drop.
func NewPreparedIdentityDrop(drop *table.DropPreparation) *preparedIdentityDrop {
	return &preparedIdentityDrop{drop: drop}
}

// AlterForeignKeyWorklist exposes the immutable scheduled work, not a catalog
// mutation or scan hook, to the composed owner's structural invariant tests.
func AlterForeignKeyWorklist(work AlterTablePhysical) ([]table.AlterForeignKeyScan, bool) {
	return work.foreignKeyHistory, work.changedValues
}
