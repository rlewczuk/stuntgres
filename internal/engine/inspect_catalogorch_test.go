package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
)

// CatalogDependencyPreflight calls catalogDependencyPreflight.
func CatalogDependencyPreflight(current *catalog.State, candidate *catalog.State, observations []session.DependencyObservation, originals map[ids.ObjectAddress]catalog.Descriptor) error {
	return catalogDependencyPreflight(current, candidate, observations, originals)
}

// CatalogDiscarded is catalogDiscarded.
const CatalogDiscarded = catalogDiscarded

// CatalogPublished is catalogPublished.
const CatalogPublished = catalogPublished

// CatalogRoleReadFence names catalogRoleReadFence.
type CatalogRoleReadFence = catalogRoleReadFence

// FenceCatalogRoleRead is fenceCatalogRoleRead.
const FenceCatalogRoleRead = fenceCatalogRoleRead

// RelationProvider returns relationProvider.
func RelationProvider() table.RelationProvider { return relationProvider }

// SetStageInitialTables sets x.initialTables.
func SetStageInitialTables(x *CatalogStage, v map[ids.ObjectAddress]*storage.Relation) {
	x.initialTables = v
}

// SetStageTerminalCleanup sets x.terminalCleanup.
func SetStageTerminalCleanup(x *CatalogStage, v bool) { x.terminalCleanup = v }

// StageRemoveObject calls x.removeObject.
func StageRemoveObject(x *CatalogStage, ref ids.ObjectRef, namespace bool) error {
	return x.removeObject(ref, namespace)
}

// EngineCaptureCatalogStageLocked calls x.captureCatalogStageLocked.
func EngineCaptureCatalogStageLocked(x *Engine, s *CatalogStage, c *session.Journal, current *catalog.State, authority *session.CatalogAuthority) error {
	return x.captureCatalogStageLocked(s, c, current, authority)
}

// EngineCatalogAuthorityPreflightLocked calls x.catalogAuthorityPreflightLocked.
func EngineCatalogAuthorityPreflightLocked(x *Engine, a *session.CatalogAuthority, c *session.Journal) error {
	return x.catalogAuthorityPreflightLocked(a, c)
}

// EngineInstallForeignCatalogLocked calls x.installForeignCatalogLocked.
func EngineInstallForeignCatalogLocked(x *Engine, ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, nextRevision ids.DefinitionRevision, prepared index.RemintedIndexStorage, oids catalog.OIDReservation) error {
	return x.installForeignCatalogLocked(ctx, next, relations, sequences, nextRevision, prepared, oids)
}

// EnginePrepareCatalogOverlayIndexes calls x.prepareCatalogOverlayIndexes.
func EnginePrepareCatalogOverlayIndexes(x *Engine, scope *StatementScope, stage *CatalogStage, c *session.Journal) error {
	return x.prepareCatalogOverlayIndexes(scope, stage, c)
}

// EngineStageCatalogChangesLocked calls x.stageCatalogChangesLocked.
func EngineStageCatalogChangesLocked(x *Engine, stage *CatalogStage, changes *session.Journal) {
	x.stageCatalogChangesLocked(stage, changes)
}

// CatalogRoleReadFenceRoles returns x.roles.
func CatalogRoleReadFenceRoles(x *catalogRoleReadFence) *access.RoleRecord { return x.roles }

// StageDomainAlter returns x.domainAlter.
func StageDomainAlter(x *CatalogStage) *typedef.DomainAlterStage { return x.domainAlter }

// StageInitialTables returns x.initialTables.
func StageInitialTables(x *CatalogStage) map[ids.ObjectAddress]*storage.Relation {
	return x.initialTables
}

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
