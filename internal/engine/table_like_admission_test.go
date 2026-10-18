package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestTableLikeSourceAdmission(t *testing.T)          { tableLikeAdmission(t, "source") }
func TestTableLikeProviderAdmission(t *testing.T)        { tableLikeAdmission(t, "default") }
func TestTableLikePartialProviderAdmission(t *testing.T) { tableLikeAdmission(t, "predicate") }

func tableLikeAdmission(t *testing.T, provider string) {
	for _, outcome := range []string{"cancel", "rollback", "delete", "replace"} {
		t.Run(fmt.Sprintf("%s/%s", provider, outcome), func(t *testing.T) {
			e := New()
			external := provider != "source"
			target, options := "src", ""
			if provider == "predicate" {
				run(t, e, "CREATE TABLE external(a INT)")
				run(t, e, "CREATE TABLE src(id INT,k INT,active BOOL)")
				run(t, e, "CREATE INDEX source_partial ON src(k) WHERE active AND ('external'::regclass IS NOT NULL)")
				target, options = "external", " INCLUDING INDEXES"
			} else {
				run(t, e, "CREATE SEQUENCE external")
				run(t, e, "CREATE TABLE src(a BIGINT DEFAULT nextval('external'))")
				if external {
					target, options = "external", " INCLUDING DEFAULTS"
				}
			}
			ref := roleTableRef(t, e, target)
			holderOp, holderDone, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer holderDone()
			holder, err := e.StartStatement(holderOp)
			if err != nil {
				t.Fatal(err)
			}
			if err := holder.gate(statement(t, "SELECT * FROM "+target)); err != nil {
				t.Fatal(err)
			}
			holderFinished := false
			defer func() {
				if !holderFinished {
					_ = holder.Finish(nil)
				}
			}()
			if err := holder.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.gate(statement(t, "CREATE TABLE clone(LIKE src"+options+")")); err != nil {
				t.Fatal(err)
			}
			stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(LIKE src"+options+")")
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			finished := false
			defer func() {
				if !finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			if provider == "predicate" {
				if len(destination.Defaults) != 0 || len(destination.CheckConstraints) != 0 {
					t.Fatal("predicate provider admission masked by another definition")
				}
				retained := false
				for _, descriptor := range stage.descriptors {
					index, ok := descriptor.(*relationDescriptor)
					if !ok || index.SQLIndex == nil {
						continue
					}
					if index.SQLIndex.Keys[0].Derived != nil {
						t.Fatal("predicate provider admission masked by an expression key")
					}
					for _, dependency := range index.SQLIndex.Predicate.Dependencies {
						retained = retained || dependency == ref
					}
					var executionDependencies []ids.ObjectRef
					if _, err := (scalarRenderer{owner: destination, dependencies: &executionDependencies}).sql(context.Background(), e.stageRead(stage), index.SQLIndex.Predicate.Execution); err != nil {
						t.Fatal(err)
					}
					for _, dependency := range executionDependencies {
						if dependency == ref {
							t.Fatal("fixture lacks analyzed-only external dependency")
						}
					}
				}
				if !retained {
					t.Fatal("analyzed predicate provider missing")
				}
			}
			result := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-result:
					case <-time.After(5 * time.Second):
						t.Error("admission worker did not join")
					}
				}
			}()
			go func() { result <- scope.acquireDefinitionProviders(stage) }()
			lockPending(t, e, 1)
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.ticket].Mode&lock.AccessExclusive != 0
			if !queued {
				t.Fatal("not queued on exact captured provider", ref)
			}
			before := e.CatalogRead()
			if outcome == "cancel" {
				cancel()
			}
			if outcome == "delete" || outcome == "replace" {
				removal := e.BeginCatalogStage(e.CatalogRead())
				source := roleTableRef(t, e, "src")
				if provider == "predicate" {
					if err := removal.Remove(roleTableRef(t, e, "source_partial")); err != nil {
						t.Fatal(err)
					}
				}
				if err := removal.Remove(source); err != nil {
					t.Fatal(err)
				}
				if external {
					if err := removal.Remove(ref); err != nil {
						t.Fatal(err)
					}
				}
				queryCreatePublish(t, e, removal)
				if outcome == "replace" {
					if external {
						if provider == "predicate" {
							run(t, e, "CREATE TABLE external(a INT)")
						} else {
							run(t, e, "CREATE SEQUENCE external START 99")
						}
					}
					run(t, e, "CREATE TABLE src(a BIGINT DEFAULT 99)")
					if roleTableRef(t, e, target) == ref {
						t.Fatal("replacement reused old identity")
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
				t.Fatal("admission did not resume")
			}
			if outcome == "rollback" {
				if err != nil {
					t.Fatal(err)
				}
				assertHeld := func() {
					lockState := e.locks.Inspect()
					entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
					held := entry != nil && entry.Holders[scope.ticket].Mode&lock.AccessShare != 0
					if !held {
						t.Fatal("provider hold lost before publication finished")
					}
				}
				assertHeld()
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); err != nil {
					t.Fatal(err)
				}
				assertHeld()
			} else {
				if err == nil {
					t.Fatal("canceled/deleted incarnation admitted")
				}
				if outcome == "cancel" && ctx.Err() != context.Canceled {
					t.Fatal(err)
				}
				if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "clone"})) != 0 {
					t.Fatal("partial clone published")
				}
			}
			_ = scope.Finish(err)
			finished = true
			lockEmpty(t, e)
		})
	}
}
