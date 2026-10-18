package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestHashIndexFinalPublicationVeto(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(3),(1),(2)", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	// Use the existing real-publication calibration seam. The veto fires after
	// actual hash-root preparation, not at a fabricated provider callback count.
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	counting := &cancelAtErrCheck{Context: firstCtx, cancel: firstCancel}
	first := indexMaintenanceStage(t, e, "t", "hn")
	prepared, err := e.ValidateCatalogStage(counting, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: first.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	polls := counting.checks.Load()
	if polls < 4 {
		t.Fatal("no real preparation/final veto", polls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finalVeto := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: polls}
	second := indexMaintenanceStage(t, e, "t", "hn")
	prepared, err = e.ValidateCatalogStage(finalVeto, second)
	if err != nil {
		t.Fatal("veto fired before root preparation", err)
	}
	before := indexMaintenanceCapture(e)
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: second.Base.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	indexMaintenanceUnchanged(t, e, before)
}

func TestHashIndexDerivedBatchFailureAndIndexSetFence(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT CHECK(n>=0))", "INSERT INTO t VALUES(1),(2)", "CREATE INDEX hn ON t USING hash(n)", "CREATE INDEX hd ON t USING hash((10/n))"} {
		run(t, e, q)
	}
	for _, q := range []string{"INSERT INTO t VALUES(3),(0),(4)", "UPDATE t SET n=0 WHERE n=2", "CREATE INDEX bad ON t USING hash((10/(n-1)))"} {
		before := indexMaintenanceCapture(e)
		if _, err := testExecute(e, context.Background(), statement(t, q), nil); err == nil {
			t.Fatal("derived provider failure succeeded", q)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	run(t, e, "INSERT INTO t VALUES(3)")
	stage := indexMaintenanceStage(t, e, "t", "hn", "hd")
	for parent := range stage.indexParents {
		stage.indexSets = map[ids.ObjectAddress]int{parent: 2}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "CREATE INDEX sibling ON t USING hash(n)")
	before := indexMaintenanceCapture(e)
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
		t.Fatal("changed index set accepted old hash rebuild")
	} else if _, ok := err.(catalog.Conflict); !ok {
		t.Fatal(err)
	}
	indexMaintenanceUnchanged(t, e, before)
}
