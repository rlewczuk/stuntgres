package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestBtreeIndexUnchangedParentEvolution(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE indexed(n INT)", "INSERT INTO indexed VALUES(2),(1)", "CREATE INDEX idx ON indexed(n)", "CREATE TABLE unrelated(n INT)"} {
		run(t, e, q)
	}
	index, root := btreeRoot(t, e, "idx")
	base := e.CatalogRead()
	ref := roleTableRef(t, e, "unrelated")
	original := base.state.descriptors[ref.Address].(*relationDescriptor)
	replacement := *original
	replacement.revision++
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	var evolutionErr error
	allocations := testing.AllocsPerRun(10, func() { e.mu.Lock(); evolutionErr = e.evolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil); e.mu.Unlock() })
	if evolutionErr != nil || allocations != 0 {
		t.Fatal("untouched parent performs evolution work", evolutionErr, allocations)
	}
	if stage.descriptors[index.address] != nil {
		t.Fatal("unrelated metadata staged an index change")
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	after, nextRoot := btreeRoot(t, e, "idx")
	if after != index || nextRoot != root {
		t.Fatal("unrelated metadata changed index authority/root")
	}
}
