package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func indexMaintenanceStage(t *testing.T, e *core.Engine, table string, indexes ...string) *core.CatalogStage {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	parent := roleTableRef(t, e, table)
	stage.Indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address: base.MustRelation(parent.Address)}
	stage.Indexes().Rebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(indexes))
	for _, name := range indexes {
		ref := roleTableRef(t, e, name)
		stage.Indexes().Rebuilds[ref.Address] = base.MustRelation(ref.Address)
	}
	return stage
}
