package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
)

// CatalogViewLeafEvents calls catalogViewLeafEvents.
func CatalogViewLeafEvents(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32) (uint32, error) {
	return (Runtime{}).catalogViewLeafEvents(ctx, read, relation, subset)
}

// EngineCapturePGPredicateLocksLocked calls x.capturePGPredicateLocksLocked.
func EngineCapturePGPredicateLocksLocked(x *Engine) ([]lock.Record, error) {
	return x.capturePGPredicateLocksLocked()
}

// EngineOpenCatalogSourceLocked calls x.openCatalogSourceLocked.
func EngineOpenCatalogSourceLocked(x *Engine, scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	return x.openCatalogSourceLocked(scope, ref, source, pgcatalog.Keep{})
}
