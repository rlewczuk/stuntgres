package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// Provider references join the incumbent dependency stage. Owner-local column
// dependencies are AUTO and are handled by DROP COLUMN's stable-slot migration.
func stageCheckDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]table.CheckDependencyEdge, error) {
	return table.CheckDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}

// stageDefaultDependents lists the column defaults that depend on targets.
func stageDefaultDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]table.DefaultDependencyEdge, error) {
	return table.DefaultDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}

// viewDependencyClosure lists the views that depend on targets.
func viewDependencyClosure(catalog catalog.Read, targets []ids.ObjectAddress, stage *CatalogStage) []view.DependencyEdge {
	return view.DependentViews(&dropProvider{base: catalog, stage: stage}, targets)
}
