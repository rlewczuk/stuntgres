package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

func notNullMetadataStage(t *testing.T, e *Engine) (*CatalogStage, ids.ObjectRef, *catalog.Relation) {
	t.Helper()
	ref := roleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	next.Columns = append([]catalog.Column(nil), old.Columns...)
	next.Columns[0].Nullable = false
	next.Constraints.NotNull = []catalog.NotNullConstraint{{Name: "t_a_not_null", Attribute: 1}}
	e.Mu.Lock()
	err := table.AssignDefinitionOIDs(DDLHost{E: e}, &next)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := e.Storage.Relations[ref.Address]
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.AlterRelation(context.Background(), ref, &next, original, original, len(original.Versions), original.Generation, original.Ends); err != nil {
		t.Fatal(err)
	}
	return stage, ref, old
}
