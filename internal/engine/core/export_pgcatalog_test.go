package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
)

// EngineCapturePGPredicateLocksLocked calls x.capturePGPredicateLocksLocked.
func EngineCapturePGPredicateLocksLocked(x *Engine) ([]lock.Record, error) {
	return x.capturePGPredicateLocksLocked()
}

// EngineOpenCatalogSourceLocked calls x.openCatalogSourceLocked.
func EngineOpenCatalogSourceLocked(x *Engine, scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation, outputUses []int) (*exec.Source, error) {
	return x.openCatalogSourceLocked(scope, ref, source, pgcatalog.Keep{}, outputUses, false, nil)
}
