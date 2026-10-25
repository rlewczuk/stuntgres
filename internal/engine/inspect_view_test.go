package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// EngineOpenRelationSourceLocked calls x.openRelationSourceLocked.
func EngineOpenRelationSourceLocked(x *Engine, scope *StatementScope, ref ids.ObjectRef, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int) (*exec.Source, error) {
	return x.openRelationSourceLocked(scope, ref, invoker, mark, locks, outputUses)
}

// ScopeAcquireMaterializedRefresh calls x.acquireMaterializedRefresh.
func ScopeAcquireMaterializedRefresh(x *StatementScope, stmt *parser.RefreshMaterializedView) error {
	return x.acquireMaterializedRefresh(stmt)
}

// MaterializedSourceFence names materializedSourceFence.
type MaterializedSourceFence = materializedSourceFence

// RowSecurityProgram names rowSecurityProgram.
type RowSecurityProgram = rowSecurityProgram

// StageMaterializedReplacementCurrent calls x.materializedReplacementCurrent.
func StageMaterializedReplacementCurrent(x *CatalogStage, snapshot *catalog.State, stored *storage.Relation) error {
	return x.materializedReplacementCurrent(snapshot, stored)
}

// EngineCaptureRowSecurityLocked calls x.captureRowSecurityLocked.
func EngineCaptureRowSecurityLocked(x *Engine, s *StatementScope, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed bool, selectRead bool) (*exec.RLSProgram, error) {
	return x.captureRowSecurityLocked(s, d, principal, command, proposed, selectRead)
}

// EngineEndRowSecurityAdmissionLocked calls x.endRowSecurityAdmissionLocked.
func EngineEndRowSecurityAdmissionLocked(x *Engine, s *StatementScope, err error) error {
	return x.endRowSecurityAdmissionLocked(s, err)
}

// EngineMaterializedSourceFenceCurrent calls x.materializedSourceFenceCurrent.
func EngineMaterializedSourceFenceCurrent(x *Engine, f materializedSourceFence) bool {
	return x.materializedSourceFenceCurrent(f)
}

// ScopeAcquireBoundViewRelationModes calls x.acquireBoundViewRelationModes.
func ScopeAcquireBoundViewRelationModes(x *StatementScope, refs []ids.ObjectRef, modes map[ids.ObjectRef]lock.Mode) error {
	return x.acquireBoundViewRelationModes(refs, modes)
}

// ScopeAcquireViewWriteTarget calls x.acquireViewWriteTarget.
func ScopeAcquireViewWriteTarget(x *StatementScope, w *view.Write) error {
	return x.acquireViewWriteTarget(w)
}

// ScopeTakeRowSecurityPrograms calls x.takeRowSecurityPrograms.
func ScopeTakeRowSecurityPrograms(x *StatementScope, c *sqlCursor) {
	x.takeRowSecurityPrograms(c)
}

// MaterializedSourceFenceRef returns x.ref.
func MaterializedSourceFenceRef(x *materializedSourceFence) ids.ObjectRef { return x.ref }

// MaterializedSourceFenceRelation returns x.relation.
func MaterializedSourceFenceRelation(x *materializedSourceFence) *catalog.Relation { return x.relation }

// MaterializedSourceFenceStorage returns x.storage.
func MaterializedSourceFenceStorage(x *materializedSourceFence) *storage.Relation { return x.storage }

// MaterializedSourceFenceFields holds the materializedSourceFence fields tests set.
type MaterializedSourceFenceFields struct {
	Ref      ids.ObjectRef
	Relation *catalog.Relation
	Storage  *storage.Relation
}

// NewMaterializedSourceFence builds a materializedSourceFence from f.
func NewMaterializedSourceFence(f MaterializedSourceFenceFields) materializedSourceFence {
	return materializedSourceFence{ref: f.Ref, relation: f.Relation, storage: f.Storage}
}

// RowSecurityProgramFields holds the rowSecurityProgram fields tests set.
type RowSecurityProgramFields struct {
	RLSProgram exec.RLSProgram
}

// NewRowSecurityProgram builds a rowSecurityProgram from f.
func NewRowSecurityProgram(f RowSecurityProgramFields) *rowSecurityProgram {
	return &rowSecurityProgram{RLSProgram: f.RLSProgram}
}

// RowSecurityProgramSources returns x.sources.
func RowSecurityProgramSources(x *rowSecurityProgram) []policySourcePreparation { return x.sources }
