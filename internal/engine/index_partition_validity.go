package engine

import (
	"context"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// stagePartitionIndexValidity runs the validity pass over the stage. It
// needs no lock.
func stagePartitionIndexValidity(e *core.Engine, stage *core.CatalogStage, promote ids.ObjectAddress) error {
	return index.PartitionIndexValidity(core.StageDropProvider(context.Background(), stage, nil, nil, nil), promote)
}
