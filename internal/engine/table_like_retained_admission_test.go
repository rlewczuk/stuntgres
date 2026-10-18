package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestTableLikeTemporaryRetainedProviderAdmission(t *testing.T) {
	for _, outcome := range []string{"cancel", "publish", "rollback", "delete", "recreate", "pointer"} {
		t.Run(outcome, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TEMP SEQUENCE seed START 31 CACHE 1")
			sessionSQL(t, owner, "CREATE TEMP TABLE src(v BIGINT DEFAULT nextval('pg_temp.seed'))")
			source := tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "src")
			provider := tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "seed")
			original := source.defaults[1]
			manifest := slices.Clone(original.dependencies)
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			run(t, e, "CREATE TABLE barrier(v INT)")
			current := e.catalog.domain.Ref(e.CatalogRead().Epoch(), provider.address)
			old := e.catalog.domain.Ref(manifest[0].Epoch, provider.address)
			for _, ref := range manifest {
				if ref.Address == provider.address {
					old = ref
				}
			}
			if old.Epoch == current.Epoch || e.CatalogRead().state.descriptors[provider.address] != provider {
				t.Fatal("fixture is not an actual retained old-epoch provider", old, current)
			}
			holderOp, holderDone, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer holderDone()
			holder, err := e.StartStatement(holderOp)
			if err != nil {
				t.Fatal(err)
			}
			holderFinished := false
			defer func() {
				if !holderFinished {
					_ = holder.Finish(nil)
				}
			}()
			if err := holder.gate(statement(t, "SELECT * FROM barrier")); err != nil {
				t.Fatal(err)
			}
			// This trusted internal holder operates on the actual manager resource; it
			// does not grant another SQL session access to the owner's TEMP namespace.
			if err := holder.acquireSequence(current, accessExclusive, true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := owner.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			finished := false
			defer func() {
				if !finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			sql := "CREATE TEMP TABLE clone(LIKE pg_temp.src INCLUDING DEFAULTS)"
			if err := scope.gate(statement(t, sql)); err != nil {
				t.Fatal(err)
			}
			stage, destination, err := tableLikeBuild(t, e, op, sql)
			defer e.DiscardCatalogStage(stage)
			if err != nil {
				t.Fatal(err)
			}
			copied := destination.defaults[1]
			assertStored := func() {
				t.Helper()
				if !reflect.DeepEqual(copied.expression, original.expression) || !slices.Equal(copied.dependencies, manifest) || !reflect.DeepEqual(source.defaults[1].expression, original.expression) || !slices.Equal(source.defaults[1].dependencies, manifest) {
					t.Fatal("acquisition rewrote stored node or manifest")
				}
			}
			assertStored()
			result := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-result:
					case <-time.After(5 * time.Second):
						t.Error("retained admission worker did not join")
					}
				}
			}()
			go func() { result <- scope.acquireDefinitionProviders(stage) }()
			lockPending(t, e, 1)
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lockResource{ObjectRef: current}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == accessShare && entry.Holders[holder.ticket].Mode&accessExclusive != 0
			obsolete := lockState.Resources[lockResource{ObjectRef: old}]
			if !queued || obsolete != nil {
				t.Fatal("old manifest was not normalized only for current-epoch acquisition", queued, obsolete)
			}
			assertStored()
			if outcome == "cancel" {
				cancel()
			}
			if outcome == "pointer" {
				replacement := *provider
				replacement.revision++
				change := e.BeginCatalogStage(e.CatalogRead())
				if err := change.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, change)
				if e.CatalogRead().state.descriptors[current.Address] != &replacement || replacement.address != provider.address || replacement.relationKind != provider.relationKind {
					t.Fatal("same-address provider replacement fixture failed")
				}
			}
			if outcome == "delete" || outcome == "recreate" {
				change := e.BeginCatalogStage(e.CatalogRead())
				if err := change.Remove(e.catalog.domain.Ref(current.Epoch, source.address)); err != nil {
					t.Fatal(err)
				}
				if err := change.Remove(current); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, change)
				if outcome == "recreate" {
					replacement := *provider
					e.mu.Lock()
					oid, allocationErr := e.allocateOIDLocked()
					e.mu.Unlock()
					if allocationErr != nil {
						t.Fatal(allocationErr)
					}
					replacement.address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
					replacement.revision = 1
					change := e.BeginCatalogStage(e.CatalogRead())
					if err := change.Create(&replacement); err != nil {
						t.Fatal(err)
					}
					change.generators = map[ids.ObjectAddress]*sequenceGenerator{replacement.address: {last: replacement.sequence.start, generation: replacement.revision}}
					queryCreatePublish(t, e, change)
					if replacement.address == provider.address {
						t.Fatal("recreated provider reused old identity")
					}
				}
			}
			if err := holder.Finish(nil); err != nil {
				t.Fatal(err)
			}
			holderFinished = true
			select {
			case err = <-result:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("retained admission did not resume")
			}
			assertStored()
			if outcome == "publish" || outcome == "rollback" {
				if err != nil {
					t.Fatal(err)
				}
				assertHold := func() {
					t.Helper()
					lockState := e.locks.Inspect()
					entry := lockState.Resources[lockResource{ObjectRef: current}]
					held := entry != nil && entry.Holders[scope.ticket].Mode&accessShare != 0
					if !held {
						t.Fatal("retained provider hold lost before statement completion")
					}
				}
				assertHold()
				if outcome == "publish" {
					prepared, err := e.ValidateCatalogStage(ctx, stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
						t.Fatal(err)
					}
					if e.CatalogRead().state.descriptors[destination.address] != destination {
						t.Fatal("candidate not published")
					}
					assertHold()
				} else {
					err = context.Canceled
					if e.CatalogRead().state.descriptors[destination.address] != nil || e.storage[destination.address] != nil {
						t.Fatal("rolled back candidate published")
					}
				}
			} else {
				if err == nil {
					t.Fatal("canceled or obsolete retained provider admitted")
				}
				if outcome == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation not propagated", err)
				}
				if outcome == "pointer" && err.Error() != "stuntgres: definition provider disappeared before publication" {
					t.Fatal("retained pointer fence not exercised", err)
				}
				if e.CatalogRead().state.descriptors[destination.address] != nil || e.storage[destination.address] != nil || e.indexRoots[destination.address] != nil {
					t.Fatal("failed admission published candidate fragments")
				}
			}
			_ = scope.Finish(err)
			finished = true
			lockEmpty(t, e)
		})
	}
}
