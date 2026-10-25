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
)

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

// CatalogRoleReadFenceRoles returns x.roles.
func CatalogRoleReadFenceRoles(x *catalogRoleReadFence) *access.RoleRecord { return x.roles }

// StageInitialTables returns x.initialTables.
func StageInitialTables(x *CatalogStage) map[ids.ObjectAddress]*storage.Relation {
	return x.initialTables
}
