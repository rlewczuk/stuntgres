package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// stagePartitionIndexValidity runs the validity pass over the stage. It
// needs no lock.
func stagePartitionIndexValidity(e *Engine, stage *CatalogStage, promote ids.ObjectAddress) error {
	return index.PartitionIndexValidity(StageDropProvider(context.Background(), stage, nil, nil, nil), promote)
}
