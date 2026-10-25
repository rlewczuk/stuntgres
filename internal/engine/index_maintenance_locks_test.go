package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// All waits are observed through the actual manager queue. Register cancellation
// and joining after session cleanup so workers finish before their sessions end.
func indexMaintenanceAsync(t *testing.T, e *core.Engine, s *session.Session, q string) (context.CancelFunc, func() error) {
	t.Helper()
	stmt := statement(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			result, executeErr := e.Execute(op, stmt, 0, nil)
			err = executeErr
			if result.Rows != nil {
				result.Rows.Close()
			}
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("index utility did not terminate during cleanup")
			}
		}
	})
	return cancel, func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("index utility did not terminate")
			return nil
		}
	}
}

// Low-level catalog publication deliberately changes metadata while a SQL
// utility sleeps. This exercises exact-incarnation/grant fences without asking
// a second SQL utility to bypass the same conflicting relation locks.
func indexMaintenancePublishIndexClone(t *testing.T, e *core.Engine, name, newName string, remove bool) {
	t.Helper()
	old, _ := btreeRoot(t, e, name)
	if old.SQLIndex.Unique {
		t.Fatal("fixture requires an ordinary index")
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if remove {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), old.Address())); err != nil {
			t.Fatal(err)
		}
	}
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	var revision ids.DefinitionRevision
	if err == nil {
		revision, err = e.Catalog.RevisionLocked()
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.ObjectAddress.ObjectID = oid
	next.ObjectName = newName
	next.ObjectRevision = revision
	next.Columns = append([]catalog.Column(nil), old.Columns...)
	for i := range next.Columns {
		next.Columns[i].OriginTable = uint32(oid)
	}
	if err := stage.Create(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
