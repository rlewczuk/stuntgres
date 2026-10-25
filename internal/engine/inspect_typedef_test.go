package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// EngineDomainStageIndexRootLocked calls x.domainStageIndexRootLocked.
func EngineDomainStageIndexRootLocked(x *Engine, stage *CatalogStage, address ids.ObjectAddress) *storage.IndexRoot {
	return x.domainStageIndexRootLocked(stage, address)
}
