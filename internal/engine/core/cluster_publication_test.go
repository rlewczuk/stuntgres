package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/util"
)

func TestClusterPublicationAtomicity(t *testing.T) {
	for _, change := range []string{"index", "routine", "epoch", "cancel"} {
		t.Run(change, func(t *testing.T) {
			e, s, parent := clusterStorageSeed(t)
			enginetest.SessionSQL(t, s, "ALTER TABLE t CLUSTER ON b")
			if change == "routine" {
				for _, q := range []string{
					"CREATE FUNCTION public.cluster_fenced_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$",
					"CREATE INDEX fenced_idx ON t(public.cluster_fenced_key(k))",
					"CREATE OR REPLACE FUNCTION public.cluster_fenced_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+1$$",
				} {
					enginetest.SessionSQL(t, s, q)
				}
			}
			pause := util.NewExecutionPause("cluster.publish")
			defer pause.Resume()
			ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			statement := enginetest.Statement(t, "CLUSTER t USING a")
			done := make(chan error, 1)
			go func() { _, err := e.Execute(op, statement, 0, nil); done <- err }()
			joined := false
			defer func() {
				cancel()
				pause.Resume()
				if !joined {
					<-done
				}
			}()
			if err = pause.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "index", "routine":
				view := e.CatalogRead()
				var replacement catalog.Descriptor
				if change == "routine" {
					ref := enginetest.CatalogRef(t, e, view, "fenced_idx")
					call := view.MustRelation(ref.Address).SQLIndex.Keys[0].Derived.Definition.(plan.SQLRoutineExpr)
					procedure, exists := view.Procedure(call.Ref.Address)
					if !exists {
						t.Fatal("missing exact routine identity")
					}
					next := *procedure
					next.ObjectRevision++
					replacement = &next
				} else {
					ref := enginetest.CatalogRef(t, e, view, "b")
					old := view.MustRelation(ref.Address)
					next := *old
					if err = table.BumpAlterRevision(old, &next); err != nil {
						t.Fatal(err)
					}
					replacement = &next
				}
				stage := e.BeginCatalogStage(view)
				defer e.DiscardCatalogStage(stage)
				if err = stage.Replace(replacement); err != nil {
					t.Fatal(err)
				}
				prepared, prepareErr := e.ValidateCatalogStage(context.Background(), stage)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: view.Epoch()}); err != nil {
					t.Fatal(err)
				}
			case "epoch":
				replacement := engine.New()
				if _, err = e.ReplaceCatalog(context.Background(), replacement, replacement.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			catalogRoot := e.Catalog.State
			store := e.Storage.Relations[parent]
			roots := e.Storage.IndexRoots
			pause.Resume()
			err = <-done
			joined = true
			var conflict catalog.Conflict
			if change == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("missing publication cancellation veto", err)
				}
			} else if !errors.As(err, &conflict) && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("missing exact publication veto", change, err)
			}
			if e.Catalog.State != catalogRoot || e.Storage.Relations[parent] != store {
				t.Fatal("failed publication installed partial state")
			}
			for a, root := range roots {
				if e.Storage.IndexRoots[a] != root {
					t.Fatal("failed publication replaced root", a)
				}
			}
		})
	}
}

func TestClusterDatabaseAtomicity(t *testing.T) {
	e, s, first := clusterStorageSeed(t)
	for _, q := range []string{"ALTER TABLE t CLUSTER ON a", "CREATE TABLE later(id int)", "INSERT INTO later VALUES(2),(1)", "CREATE INDEX later_idx ON later(id)", "ALTER TABLE later CLUSTER ON later_idx"} {
		enginetest.SessionSQL(t, s, q)
	}
	for _, q := range []string{
		"CREATE FUNCTION public.cluster_database_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$",
		"CREATE INDEX database_recipe ON t(public.cluster_database_key(k))",
		"CREATE OR REPLACE FUNCTION public.cluster_database_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+1$$",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	recipe := enginetest.CatalogRef(t, e, e.CatalogRead(), "database_recipe").Address
	oldRecipe := e.Storage.IndexRoots[recipe]
	second := enginetest.CatalogRef(t, e, e.CatalogRead(), "later").Address
	if first.ObjectID >= second.ObjectID {
		t.Fatal("fixture must select known first and second identities")
	}
	oldFirst, oldSecond := e.Storage.Relations[first], e.Storage.Relations[second]
	pause := util.NewExecutionPause("cluster.database.next")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	statement := enginetest.Statement(t, "CLUSTER")
	done := make(chan error, 1)
	go func() { _, err := e.Execute(op, statement, 0, nil); done <- err }()
	joined := false
	defer func() {
		cancel()
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	if err = pause.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if e.Storage.Relations[first] != oldFirst || e.Storage.Relations[second] != oldSecond || e.Storage.IndexRoots[recipe] == oldRecipe {
		t.Fatal("targeted first publication or heap preservation missing")
	}
	reader := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"BEGIN", "LOCK TABLE t IN ACCESS SHARE MODE NOWAIT", "ROLLBACK"} {
		enginetest.SessionSQL(t, reader, q)
	}
	cancel()
	err = <-done
	joined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.Storage.Relations[first] != oldFirst || e.Storage.Relations[second] != oldSecond || e.Storage.IndexRoots[recipe] == oldRecipe {
		t.Fatal("later cancellation retracted earlier targeted commit or changed heaps")
	}
	clusterStorageIndexes(t, e, first)
}
