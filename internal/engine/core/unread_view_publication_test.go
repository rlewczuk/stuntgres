package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// The SQL here only creates the input fixture and genuine intervening winners.
// The witness is the staged, refreshed owner graph and the publication roots.
func unreadPublicationFixture(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	for _, q := range []string{
		"CREATE TYPE unread_old AS ENUM ('active')",
		"CREATE TYPE unread_new AS ENUM ('active','dormant')",
		"CREATE TABLE unread_source(id INT, state unread_old)",
		"INSERT INTO unread_source VALUES (1,NULL)",
		"CREATE INDEX unread_index ON unread_source(id) INCLUDE(state)",
		"CREATE TABLE unread_dependency(marker INT)",
		"CREATE VIEW unread_view AS SELECT s.id FROM unread_source s CROSS JOIN unread_dependency d",
	} {
		enginetest.Run(t, e, q)
	}
	return e
}

func unreadPublicationRelation(t *testing.T, e *core.Engine, read catalog.Read, name string) *catalog.Relation {
	t.Helper()
	return read.MustRelation(enginetest.CatalogRef(t, e, read, name).Address)
}

func unreadPublicationSource(t *testing.T, v *stored.View, address ids.ObjectAddress) *plan.SourceNode {
	t.Helper()
	root := v.Parts().Query.Root
	var found *plan.SourceNode
	var visit func(plan.Node)
	visit = func(n plan.Node) {
		switch n := n.(type) {
		case *plan.ProjectNode:
			visit(n.Child)
		case *plan.JoinNode:
			visit(n.Left)
			visit(n.Right)
		case *plan.SourceNode:
			if n.Ref.Address == address {
				if found != nil {
					t.Fatal("fixture unexpectedly repeated source")
				}
				found = n
			}
		default:
			t.Fatalf("unexpected fixture node %T", n)
		}
	}
	visit(root)
	if found == nil {
		t.Fatal("missing frozen source")
	}
	return found
}

// AlterRelation records the real predecessor and row-shape observations;
// EvolveStageIndexesLocked records the affected index predecessor through the
// production ALTER TYPE owner before ReownStagedTypeGenerationsLocked refreshes
// the dependent view. The INCLUDE column makes the index genuinely affected.
// A NULL enum cell needs no invented conversion or test-only type equivalence.
func unreadPublicationStage(t *testing.T, e *core.Engine) (*core.CatalogStage, *core.PreparedCatalogPublication, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	base := e.CatalogRead()
	old := unreadPublicationRelation(t, e, base, "unread_source")
	oldView := unreadPublicationRelation(t, e, base, "unread_view")
	oldIndex := unreadPublicationRelation(t, e, base, "unread_index")
	replacement := base.MustType(base.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: old.Namespace(), Name: "unread_new"}))
	next := *old
	next.ObjectRevision++
	next.Columns = slices.Clone(old.Columns)
	next.Columns[1].Usage = replacement.SQLType.Usage()
	store := e.Storage.Relations[old.Address()]
	rows := *store
	rows.Versions = slices.Clone(store.Versions)
	for i := range rows.Versions {
		rows.Versions[i].Values = slices.Clone(store.Versions[i].Values)
		rows.Versions[i].Values[1] = sqltype.Null(replacement.SQLType)
	}
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	ref := enginetest.CatalogRef(t, e, base, "unread_source")
	if err := stage.AlterRelation(t.Context(), ref, &next, store, &rows, len(store.Versions), store.Generation, store.Ends); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err := e.EvolveStageIndexesLocked(stage, ref.Address, []index.ColumnTransformation{{Attribute: old.Attributes[1]}})
	if err == nil {
		err = e.ReownStagedTypeGenerationsLocked(stage)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	candidate := e.StageRead(stage)
	view := candidate.MustRelation(oldView.Address())
	source := candidate.MustRelation(old.Address())
	candidateIndex := candidate.MustRelation(oldIndex.Address())
	if candidateIndex == oldIndex || stage.Replacement(oldIndex.Address()) != candidateIndex || candidateIndex.Columns[1].Usage.Type != replacement.SQLType {
		t.Fatal("fixture did not stage the authentic TYPE-affected index observation")
	}
	if view == oldView || stored.ViewOf(view) == stored.ViewOf(oldView) || source.Columns[1].Usage.Type != replacement.SQLType {
		t.Fatal("fixture did not produce a real refreshed candidate")
	}
	if unreadPublicationSource(t, stored.ViewOf(view), old.Address()).Schema()[1].Usage.Type != replacement.SQLType || unreadPublicationSource(t, stored.ViewOf(oldView), old.Address()).Schema()[1].Usage.Type != old.Columns[1].Usage.Type {
		t.Fatal("refresh failed canonical source identity or mutated original")
	}
	if view.Columns[0].Usage != oldView.Columns[0].Usage || !reflect.DeepEqual(stored.ViewOf(view).Parts().Dependencies, stored.ViewOf(oldView).Parts().Dependencies) || !reflect.DeepEqual(stored.ViewOf(view).Parts().Sources, stored.ViewOf(oldView).Parts().Sources) {
		t.Fatal("refresh changed visible output, dependencies or source identities")
	}
	if e.CatalogRead().State() != base.State() || e.Storage.Relations[old.Address()] != store {
		t.Fatal("validation attached candidate")
	}
	return stage, prepared, source, view
}

func TestUnreadViewRefreshPublicationFences(t *testing.T) {
	for _, phase := range []string{"commit", "unrelated", "source", "view", "dependency", "storage_append", "storage_delete", "index", "reset", "restore", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			e := unreadPublicationFixture(t)
			original := e.CatalogRead()
			oldSource := unreadPublicationRelation(t, e, original, "unread_source")
			oldView := unreadPublicationRelation(t, e, original, "unread_view")
			oldParts := stored.ViewOf(oldView).Parts()
			oldSchema := slices.Clone(unreadPublicationSource(t, stored.ViewOf(oldView), oldSource.Address()).Schema())
			oldIndex := unreadPublicationRelation(t, e, original, "unread_index")
			oldIndexRoot := e.Storage.IndexRoots[oldIndex.Address()]
			oldStore := e.Storage.Relations[oldSource.Address()]
			oldRows := slices.Clone(oldStore.Versions)
			stage, prepared, source, view := unreadPublicationStage(t, e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			switch phase {
			case "source":
				enginetest.Run(t, e, "COMMENT ON TABLE unread_source IS 'winner'")
			case "view":
				enginetest.Run(t, e, "COMMENT ON VIEW unread_view IS 'winner'")
			case "dependency":
				enginetest.Run(t, e, "DROP TABLE unread_dependency CASCADE")
			case "storage_append":
				enginetest.Run(t, e, "INSERT INTO unread_source VALUES (2,NULL)")
			case "storage_delete":
				enginetest.Run(t, e, "DELETE FROM unread_source WHERE id=1")
			case "index":
				enginetest.Run(t, e, "REINDEX INDEX unread_index")
			case "unrelated":
				enginetest.Run(t, e, "CREATE TABLE unread_unrelated(value INT)")
			case "reset":
				if err := engine.Reset(t.Context(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := engine.Restore(t.Context(), e, strings.NewReader("CREATE TABLE restored_only(value INT);")); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			winner := e.CatalogRead()
			winnerStores := make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))
			for address, store := range e.Storage.Relations {
				winnerStores[address] = store
			}
			winnerRoots := make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots))
			for address, root := range e.Storage.IndexRoots {
				winnerRoots[address] = root
			}
			if phase == "index" && (winner.Get(oldIndex.Address()) == oldIndex || e.Storage.IndexRoots[oldIndex.Address()] == oldIndexRoot) {
				t.Fatal("REINDEX fixture did not replace its observed descriptor and authentic index root")
			}
			_, err = e.PublishPreparedCatalog(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if phase == "commit" || phase == "unrelated" {
				if err != nil {
					t.Fatal(err)
				}
				current := e.CatalogRead()
				if current.Get(source.Address()) != source || current.Get(view.Address()) != view || e.Storage.Relations[source.Address()] == oldStore {
					t.Fatal("successful refresh split schema/view/store publication")
				}
				if current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}).SQLType.CompositeMember(1).Usage.Type != source.Columns[1].Usage.Type {
					t.Fatal("row type is not the published canonical source type")
				}
				for _, descriptor := range []catalog.Descriptor{source, view} {
					key := ids.NameKey{ClassID: descriptor.Address().ClassID, Namespace: descriptor.Namespace(), Name: descriptor.Name()}
					if current.NameAddress(key) != descriptor.Address() {
						t.Fatal("successful publication split descriptor/name identity")
					}
				}
				if phase == "unrelated" {
					unrelated := unreadPublicationRelation(t, e, winner, "unread_unrelated")
					if current.Get(unrelated.Address()) != unrelated || e.Storage.Relations[unrelated.Address()] != winnerStores[unrelated.Address()] {
						t.Fatal("valid merge lost unrelated descriptor/store")
					}
				}
			} else {
				if phase == "reset" || phase == "restore" {
					if !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal("epoch fence", err)
					}
				} else if phase == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation before attachment", err)
					}
				} else {
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) {
						t.Fatal("stale publication did not retain conflict boundary", err)
					}
				}
				if e.CatalogRead().State() != winner.State() || !maps.Equal(e.Storage.Relations, winnerStores) || !maps.Equal(e.Storage.IndexRoots, winnerRoots) {
					t.Fatal("rejected refresh changed winning catalog, names, dependencies, types or stores")
				}
			}
			if original.Get(oldSource.Address()) != oldSource || original.Get(oldView.Address()) != oldView || stored.ViewOf(oldView).Parts().Query != oldParts.Query || !reflect.DeepEqual(unreadPublicationSource(t, stored.ViewOf(oldView), oldSource.Address()).Schema(), oldSchema) || !reflect.DeepEqual(oldStore.Versions[:len(oldRows)], oldRows) {
				t.Fatal("publication changed retained original graph or row backing")
			}
		})
	}
	t.Run("savepoint_discard", unreadPublicationSavepoint)
}

func unreadPublicationSavepoint(t *testing.T) {
	e := unreadPublicationFixture(t)
	original := e.CatalogRead()
	source := unreadPublicationRelation(t, e, original, "unread_source")
	view := unreadPublicationRelation(t, e, original, "unread_view")
	store := e.Storage.Relations[source.Address()]
	owner := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, owner)
	enginetest.SessionSQL(t, owner, "SAVEPOINT before_refresh")
	enginetest.SessionSQL(t, owner, "ALTER TABLE unread_source ALTER state TYPE unread_new USING state::text::unread_new")
	e.Mu.Lock()
	accepted := e.PrivateCatalogLocked(owner.Transactions.Catalog)
	acceptedStore := e.PrivateStorageLocked(owner.Transactions.Catalog, source.Address())
	e.Mu.Unlock()
	acceptedSource := accepted.MustRelation(source.Address())
	acceptedView := accepted.MustRelation(view.Address())
	acceptedSchema := slices.Clone(unreadPublicationSource(t, stored.ViewOf(acceptedView), source.Address()).Schema())
	if acceptedSource == source || acceptedView == view || acceptedStore == store || acceptedSchema[1].Usage.Type != acceptedSource.Columns[1].Usage.Type {
		t.Fatal("transaction did not own refreshed candidate")
	}
	enginetest.SessionSQL(t, owner, "SAVEPOINT after_refresh")
	enginetest.SessionSQL(t, owner, "ROLLBACK TO after_refresh")
	for range 2 {
		enginetest.SessionSQL(t, owner, "ROLLBACK TO before_refresh")
		e.Mu.Lock()
		private := e.PrivateCatalogLocked(owner.Transactions.Catalog)
		privateStore := e.PrivateStorageLocked(owner.Transactions.Catalog, source.Address())
		e.Mu.Unlock()
		if private.Get(source.Address()) != source || private.Get(view.Address()) != view || privateStore != store {
			t.Fatal("rollback did not restore exact original owner roots")
		}
	}
	enginetest.SessionSQL(t, owner, "RELEASE before_refresh")
	enginetest.TransactionEnd(t, owner, ref, true)
	current := e.CatalogRead()
	if current.Get(source.Address()) != source || current.Get(view.Address()) != view || e.Storage.Relations[source.Address()] != store {
		t.Fatal("discarded refresh resurrected on COMMIT")
	}
	if accepted.Get(source.Address()) != acceptedSource || accepted.Get(view.Address()) != acceptedView || !reflect.DeepEqual(unreadPublicationSource(t, stored.ViewOf(acceptedView), source.Address()).Schema(), acceptedSchema) {
		t.Fatal("savepoint discard mutated retained accepted artifacts")
	}
}
