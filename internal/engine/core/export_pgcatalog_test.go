package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
)

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

// SetCatalogRuntimeTemporary sets x.temporary.
func SetCatalogRuntimeTemporary(x *EngineCatalogRuntime, v ids.SessionToken) { x.temporary = v }
