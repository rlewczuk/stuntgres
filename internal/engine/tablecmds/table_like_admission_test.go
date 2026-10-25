package tablecmds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTableLikeSourceAdmission(t *testing.T)          { tableLikeAdmission(t, "source") }
func TestTableLikeProviderAdmission(t *testing.T)        { tableLikeAdmission(t, "default") }
func TestTableLikePartialProviderAdmission(t *testing.T) { tableLikeAdmission(t, "predicate") }

func tableLikeAdmission(t *testing.T, provider string) {
	for _, outcome := range []string{"cancel", "rollback", "delete", "replace"} {
		t.Run(fmt.Sprintf("%s/%s", provider, outcome), func(t *testing.T) {
			e := engine.New()
			external := provider != "source"
			target, options := "src", ""
			if provider == "predicate" {
				enginetest.Run(t, e, "CREATE TABLE external(a INT)")
				enginetest.Run(t, e, "CREATE TABLE src(id INT,k INT,active BOOL)")
				enginetest.Run(t, e, "CREATE INDEX source_partial ON src(k) WHERE active AND ('external'::regclass IS NOT NULL)")
				target, options = "external", " INCLUDING INDEXES"
			} else {
				enginetest.Run(t, e, "CREATE SEQUENCE external")
				enginetest.Run(t, e, "CREATE TABLE src(a BIGINT DEFAULT nextval('external'))")
				if external {
					target, options = "external", " INCLUDING DEFAULTS"
				}
			}
			ref := enginetest.RoleTableRef(t, e, target)
			holderOp, holderDone, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer holderDone()
			holder, err := e.StartStatement(holderOp)
			if err != nil {
				t.Fatal(err)
			}
			if err := holder.Gate(enginetest.Statement(t, "SELECT * FROM "+target)); err != nil {
				t.Fatal(err)
			}
			holderFinished := false
			defer func() {
				if !holderFinished {
					_ = holder.Finish(nil)
				}
			}()
			if err := holder.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Gate(enginetest.Statement(t, "CREATE TABLE clone(LIKE src"+options+")")); err != nil {
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
				if len(destination.ColumnMeta.Defaults) != 0 || len(destination.Constraints.Check) != 0 {
					t.Fatal("predicate provider admission masked by another definition")
				}
				retained := false
				for _, descriptor := range stage.Descriptors() {
					index, ok := descriptor.(*catalog.Relation)
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
					if _, err := (stored.ScalarRenderer{Owner: destination, Dependencies: &executionDependencies}).SQL(context.Background(), e.StageRead(stage), index.SQLIndex.Predicate.Execution); err != nil {
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
			go func() { result <- scope.AcquireDefinitionProviders(stage) }()
			enginetest.LockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.LockTicket].Mode&lock.AccessExclusive != 0
			if !queued {
				t.Fatal("not queued on exact captured provider", ref)
			}
			before := e.CatalogRead()
			if outcome == "cancel" {
				cancel()
			}
			if outcome == "delete" || outcome == "replace" {
				removal := e.BeginCatalogStage(e.CatalogRead())
				source := enginetest.RoleTableRef(t, e, "src")
				if provider == "predicate" {
					if err := removal.Remove(enginetest.RoleTableRef(t, e, "source_partial")); err != nil {
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
							enginetest.Run(t, e, "CREATE TABLE external(a INT)")
						} else {
							enginetest.Run(t, e, "CREATE SEQUENCE external START 99")
						}
					}
					enginetest.Run(t, e, "CREATE TABLE src(a BIGINT DEFAULT 99)")
					if enginetest.RoleTableRef(t, e, target) == ref {
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
					lockState := e.Locks.Inspect()
					entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
					held := entry != nil && entry.Holders[scope.LockTicket].Mode&lock.AccessShare != 0
					if !held {
						t.Fatal("provider hold lost before publication finished")
					}
				}
				assertHeld()
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); err != nil {
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
			enginetest.LockEmpty(t, e)
		})
	}
}

func tableLikeBuild(t *testing.T, e *core.Engine, op *session.Operation, query string) (*core.CatalogStage, *catalog.Relation, error) {
	t.Helper()
	syntax := enginetest.Statement(t, query).(*parser.CreateTable)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if syntax.Persistence == "temporary" {
		namespace = op.Session.TemporaryNamespace
	}
	err = engine.SchemaTableLocked(e, op, auth, stage, syntax, namespace, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	var destination *catalog.Relation
	if err == nil {
		address, _ := stage.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: syntax.Name.Parts[len(syntax.Name.Parts)-1].Text})
		destination = stage.Get(address).(*catalog.Relation)
	}
	return stage, destination, err
}
