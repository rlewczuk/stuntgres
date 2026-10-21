package engine

import (
	"context"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Provider references join the incumbent dependency stage. Owner-local column
// dependencies are AUTO and are handled by DROP COLUMN's stable-slot migration.
func stageCheckDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]table.CheckDependencyEdge, error) {
	return table.CheckDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}
