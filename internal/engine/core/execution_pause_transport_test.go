package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
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

func TestExecutionPauseNestedOperationIsolation(t *testing.T) {
	e := engine.New()
	pause := util.NewExecutionPause("encoding.convert")
	defer pause.Resume()
	calls := 0
	request := workadmission.With(util.WithExecutionPause(context.Background(), pause), func(context.Context, workadmission.Class) error { calls++; return nil })
	inherited := util.ActivateExecutionPause(workadmission.Activate(request))
	op, cleanup, err := enginetest.Operation(e, inherited)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if op.SourceContext != inherited {
		t.Fatal("operation lost original source authority")
	}
	// New operation capture must inherit both requests, but neither activation.
	if _, err = sqltype.ConvertBinaryEncoding(op.Context(), strings.Repeat("a", 8192), "UTF8", "LATIN1"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("nested operation invoked inherited admission", calls)
	}
	pause.Resume()
	if err = pause.Wait(context.Background()); err == nil {
		t.Fatal("metadata operation admitted closed pause")
	}
	cause := errors.New("nested child cancellation")
	op.Cancel(cause)
	if context.Cause(op.Context()) != cause || inherited.Err() != nil {
		t.Fatal("nested cancellation changed source authority")
	}
}

func TestExecutionPauseRuntimeEffectiveChildRefusal(t *testing.T) {
	e := engine.New()
	pause := util.NewExecutionPause("encoding.convert")
	defer pause.Resume()
	type valueKey struct{}
	refusal := errors.New("actual effective child refused")
	var supplied context.Context
	policy := func(ctx context.Context, class workadmission.Class) error {
		if class != workadmission.WorkEncodingConvert || ctx.Value(valueKey{}) != "child" || ctx.Done() != supplied.Done() || context.Cause(ctx) != context.Cause(supplied) {
			return errors.New("wrong work or effective child")
		}
		return refusal
	}
	request := workadmission.With(util.WithExecutionPause(context.Background(), pause), policy)
	op, cleanup, err := enginetest.Operation(e, request)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	if err = scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	child, stop := context.WithTimeout(context.WithValue(op.Context(), valueKey{}, "child"), 5*time.Second)
	defer stop()
	supplied = child
	active := scope.ActivateRuntime(child)
	done := make(chan error, 1)
	go func() {
		_, err := sqltype.ConvertBinaryEncoding(active, strings.Repeat("a", 8192), "UTF8", "LATIN1")
		done <- err
	}()
	joined := false
	defer func() {
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	if err = pause.Wait(child); err != nil {
		t.Fatal(err)
	}
	pause.Resume()
	err = <-done
	joined = true
	if err != refusal {
		t.Fatal("runtime changed effective-child refusal identity", err)
	}
	if child.Err() != nil || op.Context().Err() != nil {
		t.Fatal("work refusal manufactured cancellation")
	}
	if err = scope.Finish(refusal); err != refusal {
		t.Fatal("publication changed refusal identity", err)
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
