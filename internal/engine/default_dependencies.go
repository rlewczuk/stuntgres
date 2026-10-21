package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func stageDefaultDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]defaultDependencyEdge, error) {
	return defaultDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}
