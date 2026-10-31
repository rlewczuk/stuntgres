package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestExecutionPauseRuntimeReservationHandoff(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE pause_handoff(id integer)")
	pause := util.NewExecutionPause("encoding.convert")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
	defer cancel()
	op, cleanup, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	if err = scope.Gate(enginetest.Statement(t, "INSERT INTO pause_handoff VALUES(1)")); err != nil {
		t.Fatal(err)
	}
	if !scope.ReplacementRead {
		t.Fatal("real implicit command did not reserve replacement")
	}
	current := e.CatalogRead()
	done := make(chan error, 1)
	active := scope.ActivateRuntime(op.Context())
	go func() { _, err := sqltype.ConvertBinaryEncoding(active, "abc", "UTF8", "LATIN1"); done <- err }()
	joined := false
	defer func() {
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	if err = pause.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if scope.ReplacementRead || !e.ReplacementMu.TryLock() {
		t.Fatal("closed-only pause retained replacement reservation")
	}
	e.ReplacementMu.Unlock()
	if !e.Mu.TryLock() {
		t.Fatal("paused provider retained engine mutex")
	}
	e.Mu.Unlock()
	if !op.Session.Mu.TryLock() {
		t.Fatal("paused provider retained session mutex")
	}
	op.Session.Mu.Unlock()
	// Install a genuine new epoch while the old admitted owner is parked.
	if _, err = e.ReplaceCatalog(ctx, e, current); err != nil {
		t.Fatal("replacement handoff", err)
	}
	replacement := e.CatalogRead().State()
	pause.Resume()
	err = <-done
	joined = true
	if err != nil {
		t.Fatal("pure provider resume", err)
	}
	if err = scope.Finish(nil); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("retired statement publication", err)
	}
	if e.CatalogRead().State() != replacement {
		t.Fatal("retired statement replaced accepted catalog")
	}
}

func TestExecutionPauseCommitProjection(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{
		"BEGIN",
		"CREATE TEMP TABLE pause_commit_provider(id integer) ON COMMIT DROP",
		"CREATE TEMP TABLE pause_commit_survivor(a integer,g boolean GENERATED ALWAYS AS('pause_commit_provider'::regclass IS NOT NULL) STORED,value text)",
		"INSERT INTO pause_commit_survivor(a,value) VALUES(1,'alpha'),(2,'alpine')",
		"CREATE INDEX pause_commit_index ON pause_commit_survivor(a) WHERE value LIKE 'a%'",
	} {
		enginetest.SessionSQL(t, owner, sql)
	}
	before := e.CatalogRead().State()
	pause := util.NewExecutionPause("text.like.match")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
	defer cancel()
	op, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	// Actual ON COMMIT DROP preserves surviving key semantics and transports
	// retained index entries. It must not invent provider demand to synchronize.
	if _, err = e.Execute(op, enginetest.Statement(t, "COMMIT"), 0, nil); err != nil {
		t.Fatal("retained layout commit", err)
	}
	pause.Resume()
	if err = pause.Wait(ctx); err == nil {
		t.Fatal("layout-only projection recomputed retained predicate")
	}
	if e.CatalogRead().State() == before {
		t.Fatal("successful COMMIT did not publish projected candidate")
	}
	var parent, idx ids.ObjectAddress
	for address, relation := range e.CatalogRead().Relations() {
		switch relation.Name() {
		case "pause_commit_provider":
			t.Fatal("ON COMMIT DROP retained provider")
		case "pause_commit_survivor":
			parent = address
		case "pause_commit_index":
			idx = address
		}
	}
	store, root := e.Storage.Relations[parent], e.Storage.IndexRoots[idx]
	if store == nil || len(store.Versions) != 2 || root == nil || len(root.Entries) != 2 {
		t.Fatal("retained projection lost real heap/index roots", store, root)
	}
}
