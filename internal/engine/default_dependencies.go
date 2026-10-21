package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func stageDefaultDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]table.DefaultDependencyEdge, error) {
	return table.DefaultDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}
