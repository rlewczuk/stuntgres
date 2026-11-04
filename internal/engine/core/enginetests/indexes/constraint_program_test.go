package indexes_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func TestPartitionConstraintCapturedProgram(t *testing.T) {
	for _, mode := range []string{"foreign", "stale", "consume_once"} {
		t.Run(mode, func(t *testing.T) {
			_, _, _, stage := partitionProjectionStage(t, false, "ALTER TABLE",
				"CREATE TABLE projection_root(k INT PRIMARY KEY) PARTITION BY RANGE(k)",
				"CREATE TABLE projection_child PARTITION OF projection_root DEFAULT")
			view := stage.StageRead()
			root := partitionProjectionRelation(t, stage, "projection_root")
			captured, err := index.PreparePartitionConstraintDrop(view, view.Descriptors(), view.Get, root.Constraints.Unique[0].OID)
			if err != nil || len(captured.Indexes) != 1 {
				t.Fatal("actual selected inherited cohort", err)
			}
			idx := captured.Indexes[0]
			switch mode {
			case "foreign":
				peer := engine.New()
				foreign := peer.BeginCatalogStage(peer.CatalogRead())
				defer peer.DiscardCatalogStage(foreign)
				if err := captured.Apply(foreign); err != ids.ErrCatalogReplaced {
					t.Fatal("foreign domain adopted captured program", err)
				}
				if stage.StageRead().Get(idx.Address()) != idx {
					t.Fatal("foreign apply changed source")
				}
			case "stale":
				next := *idx
				next.ObjectRevision++
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				if err := captured.Apply(stage); err != ids.ErrCatalogReplaced {
					t.Fatal("changed backing descriptor accepted", err)
				}
				if stage.RemovalCount() != 0 {
					t.Fatal("stale program removed a prefix")
				}
			case "consume_once":
				if err := captured.Apply(stage); err != nil {
					t.Fatal(err)
				}
				if stage.StageRead().Get(idx.Address()) != nil {
					t.Fatal("captured physical backing removal missing")
				}
				child := stage.StageRead().Get(idx.SQLIndex.Parent).(*catalog.Relation)
				if len(child.Constraints.Unique) != 0 {
					t.Fatal("captured inherited constraint projection missing")
				}
				if err := captured.Apply(stage); err != ids.ErrCatalogReplaced {
					t.Fatal("duplicate physical program accepted", err)
				}
			}
		})
	}
}
