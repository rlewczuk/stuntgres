package indexcmds_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestHashIndexFinalPublicationVeto(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(3),(1),(2)", "CREATE INDEX hn ON t USING hash(n)"} {
		enginetest.Run(t, e, q)
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
	if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: first.Base.Epoch()}); err != nil {
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
	if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: second.Base.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	indexMaintenanceUnchanged(t, e, before)
}

func TestHashIndexDerivedBatchFailureAndIndexSetFence(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE t(n INT CHECK(n>=0))", "INSERT INTO t VALUES(1),(2)", "CREATE INDEX hn ON t USING hash(n)", "CREATE INDEX hd ON t USING hash((10/n))"} {
		enginetest.Run(t, e, q)
	}
	for _, q := range []string{"INSERT INTO t VALUES(3),(0),(4)", "UPDATE t SET n=0 WHERE n=2", "CREATE INDEX bad ON t USING hash((10/(n-1)))"} {
		before := indexMaintenanceCapture(e)
		if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err == nil {
			t.Fatal("derived provider failure succeeded", q)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	enginetest.Run(t, e, "INSERT INTO t VALUES(3)")
	stage := indexMaintenanceStage(t, e, "t", "hn", "hd")
	for parent := range stage.IndexesOrZero().Parents {
		stage.Indexes().Sets = map[ids.ObjectAddress]int{parent: 2}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "CREATE INDEX sibling ON t USING hash(n)")
	before := indexMaintenanceCapture(e)
	if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
		t.Fatal("changed index set accepted old hash rebuild")
	} else if _, ok := err.(catalog.Conflict); !ok {
		t.Fatal(err)
	}
	indexMaintenanceUnchanged(t, e, before)
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
