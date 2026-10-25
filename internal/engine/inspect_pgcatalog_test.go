package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
)

// CatalogSourceTemporaryOwner calls catalogSourceTemporaryOwner.
func CatalogSourceTemporaryOwner(auth AuthorizationView) ids.SessionToken {
	return catalogSourceTemporaryOwner(auth)
}

// CatalogViewLeafEvents calls catalogViewLeafEvents.
func CatalogViewLeafEvents(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32) (uint32, error) {
	return catalogViewLeafEvents(ctx, read, relation, subset)
}

// EngineCapturePGPredicateLocksLocked calls x.capturePGPredicateLocksLocked.
func EngineCapturePGPredicateLocksLocked(x *Engine) ([]lock.Record, error) {
	return x.capturePGPredicateLocksLocked()
}

// EngineOpenCatalogSourceLocked calls x.openCatalogSourceLocked.
func EngineOpenCatalogSourceLocked(x *Engine, scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	return x.openCatalogSourceLocked(scope, ref, source)
}

// CatalogRuntimeBuiltinViewDefinition calls x.builtinViewDefinition.
func CatalogRuntimeBuiltinViewDefinition(x EngineCatalogRuntime, ctx context.Context, read catalog.Read, relation *catalog.Relation) (string, error) {
	return x.builtinViewDefinition(ctx, read, relation)
}

// CatalogRuntimePresentationRef returns &x.presentation.
func CatalogRuntimePresentationRef(x *EngineCatalogRuntime) *stored.Presentation {
	return &x.presentation
}

// CatalogRuntimeFields holds the EngineCatalogRuntime fields tests set.
type CatalogRuntimeFields struct {
	Engine       *Engine
	Auth         AuthorizationView
	Cursors      *cursorRegistry
	Settings     *session.SettingsStore
	Registry     settings.Registry
	Temporary    ids.SessionToken
	Presentation stored.Presentation
	SequenceBase *catalog.State
	SequenceOwn  *session.Journal
}

// NewCatalogRuntime builds a EngineCatalogRuntime from f.
func NewCatalogRuntime(f CatalogRuntimeFields) EngineCatalogRuntime {
	return EngineCatalogRuntime{engine: f.Engine, Auth: f.Auth, cursors: f.Cursors, settings: f.Settings, registry: f.Registry, temporary: f.Temporary, presentation: f.Presentation, sequenceBase: f.SequenceBase, sequenceOwn: f.SequenceOwn}
}

// SetCatalogRuntimeTemporary sets x.temporary.
func SetCatalogRuntimeTemporary(x *EngineCatalogRuntime, v ids.SessionToken) { x.temporary = v }
