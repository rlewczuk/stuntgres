// Inspection accessors: exported views of unexported core members for tests
// beside family code. Core tests reach core internals through the
// export_*_test.go accessors, which a test in another package cannot see, so
// it uses these. Add one only when a test can assert neither over the
// exported API nor through SQL, and remove it once no test uses it.

package core

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CatalogDependencyPreflight calls catalogDependencyPreflight.
func CatalogDependencyPreflight(current *catalog.State, candidate *catalog.State, observations []session.DependencyObservation, originals map[ids.ObjectAddress]catalog.Descriptor) error {
	return catalogDependencyPreflight(current, candidate, observations, originals)
}

// CatalogDiscarded is catalogDiscarded.
const CatalogDiscarded = catalogDiscarded

// CatalogPublished is catalogPublished.
const CatalogPublished = catalogPublished

// StageRemoveObject calls x.removeObject.
func StageRemoveObject(x *CatalogStage, ref ids.ObjectRef, namespace bool) error {
	return x.removeObject(ref, namespace)
}

// EngineCaptureCatalogStageLocked calls x.captureCatalogStageLocked.
func EngineCaptureCatalogStageLocked(x *Engine, s *CatalogStage, c *session.Journal, current *catalog.State, authority *session.CatalogAuthority) error {
	return x.captureCatalogStageLocked(s, c, current, authority)
}

// EngineStageCatalogChangesLocked calls x.stageCatalogChangesLocked.
func EngineStageCatalogChangesLocked(x *Engine, stage *CatalogStage, changes *session.Journal) {
	x.stageCatalogChangesLocked(stage, changes)
}

// StageDomainAlter returns x.domainAlter.
func StageDomainAlter(x *CatalogStage) *typedef.DomainAlterStage { return x.domainAlter }

// EnginePreparePrivateCatalog calls x.preparePrivateCatalog.
func EnginePreparePrivateCatalog(x *Engine, ctx context.Context, c *session.Journal, epoch ids.CatalogEpoch) (*catalog.State, error) {
	return x.preparePrivateCatalog(ctx, c, epoch)
}

// PreparedCatalogPublicationEpoch returns x.epoch.
func PreparedCatalogPublicationEpoch(x *PreparedCatalogPublication) ids.CatalogEpoch { return x.epoch }

// PreparedCatalogPublicationStage returns x.stage.
func PreparedCatalogPublicationStage(x *PreparedCatalogPublication) *CatalogStage { return x.stage }

// SetPreparedCatalogPublicationCtx sets x.ctx.
func SetPreparedCatalogPublicationCtx(x *PreparedCatalogPublication, v context.Context) { x.ctx = v }

// CatalogRoleReadFenceFields holds the catalogRoleReadFence fields tests set.
type CatalogRoleReadFenceFields struct {
	Roles *access.RoleRecord
}

// NewCatalogRoleReadFence builds a catalogRoleReadFence from f.
func NewCatalogRoleReadFence(f CatalogRoleReadFenceFields) catalogRoleReadFence {
	return catalogRoleReadFence{roles: f.Roles}
}

// ScopeAcquireRow calls x.acquireRow.
func ScopeAcquireRow(x *StatementScope, candidate visibility.DeleteCandidate) error {
	return x.acquireRow(candidate)
}

// EnginePreparedRef returns &x.prepared.
func EnginePreparedRef(x *Engine) *session.PreparedRegistry { return &x.prepared }

// PartitionReplayAllowed calls partitionReplayAllowed.
func PartitionReplayAllowed(op *session.Operation, auth AuthorizationView) func(parser.Statement, *catalog.Relation) bool {
	return partitionReplayAllowed(op, auth)
}

// CatalogRuntimeFields holds the EngineCatalogRuntime fields tests set.
type CatalogRuntimeFields struct {
	Engine       *Engine
	Auth         AuthorizationView
	Cursors      *cursorRegistry
	Settings     *session.SettingsStore
	Registry     settings.Registry
	Input        sqltype.InputEnvironment
	Database     string
	Temporary    ids.SessionToken
	Presentation stored.Presentation
	SequenceBase *catalog.State
	SequenceOwn  *session.Journal
}

// NewCatalogRuntime builds a EngineCatalogRuntime from f.
func NewCatalogRuntime(f CatalogRuntimeFields) EngineCatalogRuntime {
	return EngineCatalogRuntime{engine: f.Engine, Auth: f.Auth, cursors: f.Cursors, settings: f.Settings, registry: f.Registry, input: f.Input, Database: f.Database, temporary: f.Temporary, presentation: f.Presentation, sequenceBase: f.SequenceBase, sequenceOwn: f.SequenceOwn}
}

// AuthorizationMaterializedPopulation returns x.materializedPopulation.
func AuthorizationMaterializedPopulation(x *AuthorizationView) bool { return x.materializedPopulation }

// StageValidateRoleCandidate calls x.validateRoleCandidate.
func StageValidateRoleCandidate(x *CatalogStage, current *access.RoleRecord) error {
	return x.validateRoleCandidate(current)
}

// EnginePublishDatabaseFrozenXIDLocked calls x.publishDatabaseFrozenXIDLocked.
func EnginePublishDatabaseFrozenXIDLocked(x *Engine) {
	x.publishDatabaseFrozenXIDLocked()
}

// EngineRefreshOperationRolesLocked calls x.refreshOperationRolesLocked.
func EngineRefreshOperationRolesLocked(x *Engine, op *session.Operation) error {
	return x.refreshOperationRolesLocked(op)
}

// EngineRoleRetirementPreflightLocked calls x.roleRetirementPreflightLocked.
func EngineRoleRetirementPreflightLocked(x *Engine, c *session.Journal, state *catalog.State) error {
	return x.roleRetirementPreflightLocked(c, state)
}

// ProcedureReturnsSet calls procedureReturnsSet.
func ProcedureReturnsSet(d *catalog.Procedure) bool {
	return procedureReturnsSet(d)
}

// PartitionCheckReplayAllowed calls partitionCheckReplayAllowed.
func PartitionCheckReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, d *catalog.Relation) bool {
	return partitionCheckReplayAllowed(op, auth, raw, d)
}

// PartitionUsingIndexReplayAllowed calls partitionUsingIndexReplayAllowed.
func PartitionUsingIndexReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	return partitionUsingIndexReplayAllowed(op, auth, raw, table)
}

// StageValidateIndexMaintenance calls x.validateIndexMaintenance.
func StageValidateIndexMaintenance(x *CatalogStage, current *catalog.State) error {
	return x.validateIndexMaintenance(current)
}

// EnginePrepareQueryTableLocked calls x.prepareQueryTableLocked.
func EnginePrepareQueryTableLocked(x *Engine, op *session.Operation, auth AuthorizationView, s *parser.CreateQueryTable, b analyze.Binding) (*CatalogStage, *catalog.Relation, bool, error) {
	return x.prepareQueryTableLocked(op, auth, s, b)
}

// StageTableOrZero calls x.tableOrZero.
func StageTableOrZero(x *CatalogStage) table.Stage {
	return x.tableOrZero()
}

// StageValidateLikeSources calls x.validateLikeSources.
func StageValidateLikeSources(x *CatalogStage, current catalog.Read) error {
	return x.validateLikeSources(current)
}

// CaptureSavepoint calls captureSavepoint.
func CaptureSavepoint(t *session.TransactionOwner, name string) session.SavepointMark {
	return captureSavepoint(t, name)
}

// RestoreSavepoint calls restoreSavepoint.
func RestoreSavepoint(t *session.TransactionOwner, mark session.SavepointMark, settings session.SettingsMark) {
	restoreSavepoint(t, mark, settings)
}

// ScopeSnapshotPhase calls x.snapshotPhase.
func ScopeSnapshotPhase(x *StatementScope, stmt parser.Statement) error {
	return x.snapshotPhase(stmt)
}

// SetScopeWorked sets x.worked.
func SetScopeWorked(x *StatementScope, v bool) { x.worked = v }

// ScopeExecuteSavepoint calls x.executeSavepoint.
func ScopeExecuteSavepoint(x *StatementScope, control *parser.SavepointControl, count int) (Result, error) {
	return x.executeSavepoint(control, count)
}

// EngineOpenRelationSourceLocked calls x.openRelationSourceLocked.
func EngineOpenRelationSourceLocked(x *Engine, scope *StatementScope, ref ids.ObjectRef, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int) (*exec.Source, error) {
	return x.openRelationSourceLocked(scope, ref, invoker, mark, locks, outputUses, pgcatalog.Keep{})
}

// ScopeAcquireMaterializedRefresh calls x.acquireMaterializedRefresh.
func ScopeAcquireMaterializedRefresh(x *StatementScope, stmt *parser.RefreshMaterializedView) error {
	return x.acquireMaterializedRefresh(stmt)
}

// StageMaterializedReplacementCurrent calls x.materializedReplacementCurrent.
func StageMaterializedReplacementCurrent(x *CatalogStage, snapshot *catalog.State, stored *storage.Relation) error {
	return x.materializedReplacementCurrent(snapshot, stored)
}

// EngineMaterializedSourceFenceCurrent calls x.materializedSourceFenceCurrent.
func EngineMaterializedSourceFenceCurrent(x *Engine, f materializedSourceFence) bool {
	return x.materializedSourceFenceCurrent(f)
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

// MaterializedSourceFences returns c's materialized-view source fences.
func MaterializedSourceFences(c *session.Journal) []materializedSourceFence {
	var out []materializedSourceFence
	for _, f := range c.Fences {
		if fence, ok := f.(materializedSourceFence); ok {
			out = append(out, fence)
		}
	}
	return out
}
