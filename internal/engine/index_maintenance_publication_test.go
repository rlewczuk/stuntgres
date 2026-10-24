package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func indexMaintenanceStage(t *testing.T, e *Engine, table string, indexes ...string) *CatalogStage {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	parent := roleTableRef(t, e, table)
	stage.indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address: base.MustRelation(parent.Address)}
	stage.indexes().Rebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(indexes))
	for _, name := range indexes {
		ref := roleTableRef(t, e, name)
		stage.indexes().Rebuilds[ref.Address] = base.MustRelation(ref.Address)
	}
	return stage
}

func TestIndexMaintenanceDescriptorUnchangedIntent(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(1),(2),(NULL)", "CREATE INDEX a ON t(n)", "CREATE INDEX b ON t(n DESC)"} {
		run(t, e, q)
	}
	a, old := btreeRoot(t, e, "a")
	b, sibling := btreeRoot(t, e, "b")
	parent := e.catalog.State.Get(a.SQLIndex.Parent)
	store := e.storage.Relations[a.SQLIndex.Parent]
	stage := indexMaintenanceStage(t, e, "t", "a")
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if e.storage.IndexRoots[a.Address()] != old {
		t.Fatal("validation installed a root")
	}
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	if err != nil || len(changes.Definitions) != 0 {
		t.Fatal("descriptor-unchanged rebuild forged a definition change", changes, err)
	}
	if e.catalog.State.Get(a.Address()) != a || e.catalog.State.Get(a.SQLIndex.Parent) != parent || e.storage.Relations[a.SQLIndex.Parent] != store || e.storage.IndexRoots[a.Address()] == old || e.storage.IndexRoots[b.Address()] != sibling || !reflect.DeepEqual(e.storage.IndexRoots[a.Address()].Entries, old.Entries) {
		t.Fatal("selected intent did not publish exactly one real root")
	}
}

func TestIndexMaintenanceStageIncarnationAndIndexSetFences(t *testing.T) {
	for _, action := range []string{"recreate", "new_index", "parent_revision", "reset"} {
		t.Run(action, func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX a ON t(n)"} {
				run(t, e, q)
			}
			stage := indexMaintenanceStage(t, e, "t", "a")
			for parent := range stage.indexesOrZero().Parents {
				stage.indexes().Sets = map[ids.ObjectAddress]int{parent: 1}
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "recreate":
				run(t, e, "DROP INDEX a")
				run(t, e, "CREATE INDEX a ON t(n)")
			case "new_index":
				run(t, e, "CREATE INDEX b ON t(n)")
			case "parent_revision":
				base := e.CatalogRead()
				ref := roleTableRef(t, e, "t")
				d := *base.MustRelation(ref.Address)
				d.ObjectRevision++
				replacement := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(replacement)
				if err := replacement.Replace(&d); err != nil {
					t.Fatal(err)
				}
				p, err := e.ValidateCatalogStage(context.Background(), replacement)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()}); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			before := indexMaintenanceCapture(e)
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if action == "reset" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("epoch replacement accepted old rebuild", err)
				}
			} else if _, ok := err.(catalog.Conflict); !ok {
				t.Fatal("changed observation accepted old rebuild", err)
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}

func TestIndexMaintenanceFinalPublicationCancellation(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(3),(1),(2)", "CREATE INDEX a ON t(n)"} {
		run(t, e, q)
	}
	// Measure this real provider/publication's polling path, not a hard-coded
	// implementation callback count. Cancel the second publication at its last
	// checked boundary, after its real root has been extracted and sorted.
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	counting := &cancelAtErrCheck{Context: firstCtx, cancel: firstCancel}
	first := indexMaintenanceStage(t, e, "t", "a")
	prepared, err := e.ValidateCatalogStage(counting, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: first.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	polls := counting.checks.Load()
	if polls < 4 {
		t.Fatal("publication lacked real root preparation and final polling", polls)
	}
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	finalVeto := &cancelAtErrCheck{Context: secondCtx, cancel: secondCancel, at: polls}
	second := indexMaintenanceStage(t, e, "t", "a")
	prepared, err = e.ValidateCatalogStage(finalVeto, second)
	if err != nil {
		t.Fatal("cancellation fired before root preparation", err)
	}
	before := indexMaintenanceCapture(e)
	_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: second.Base.Epoch()})
	if !errors.Is(err, context.Canceled) || finalVeto.checks.Load() != polls {
		t.Fatal("final publication veto was not honored", polls, finalVeto.checks.Load(), err)
	}
	indexMaintenanceUnchanged(t, e, before)
}

func TestIndexMaintenancePreservesNewestUnchangedSibling(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX a ON t(n)", "CREATE INDEX b ON t(n)"} {
		run(t, e, q)
	}
	a, oldA := btreeRoot(t, e, "a")
	b, oldB := btreeRoot(t, e, "b")
	stage := indexMaintenanceStage(t, e, "t", "a")
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "INSERT INTO t VALUES(2)")
	newestB := e.storage.IndexRoots[b.Address()]
	if newestB == oldB {
		t.Fatal("fixture did not publish a newer sibling")
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if e.storage.IndexRoots[a.Address()] == oldA || e.storage.IndexRoots[b.Address()] != newestB {
		t.Fatal("selected publication overwrote a newest unchanged sibling")
	}
	btreeWantKeys(t, e, "a", "1", "2")
	btreeWantKeys(t, e, "b", "1", "2")
}

func TestIndexMaintenanceFinalAuthorizationFence(t *testing.T) {
	for _, change := range []string{"role_registry", "session_identity"} {
		t.Run(change, func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX a ON t(n)", "CREATE USER other"} {
				run(t, e, q)
			}
			op, done := roleOperation(t, e, "postgres")
			defer done()
			stage := indexMaintenanceStage(t, e, "t", "a")
			op.Session.Mu.Lock()
			stage.authSession, stage.authRevision = op.Session, op.Session.Authorization.Revision
			op.Session.Mu.Unlock()
			e.mu.Lock()
			stage.roleRevision = e.roles.published.Revision
			e.mu.Unlock()
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if change == "role_registry" {
				run(t, e, "CREATE ROLE unrelated")
			} else {
				roleExecute(t, e, op, "SET SESSION AUTHORIZATION other")
			}
			before := indexMaintenanceCapture(e)
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil || err.Error() != "stuntgres: authorization changed before publication" {
				t.Fatal("final authorization fence accepted stale authority", err)
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}
