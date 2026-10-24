package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func transactionalSchemaUnitStage(t *testing.T, e *Engine, op *session.Operation, name string, ref ids.TxRef, value int64) (*CatalogStage, *catalog.Relation) {
	t.Helper()
	usage := testUsage(e, sqltype.Int4)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	d, err := table.NewRelation(ddlHost{e: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name, auth.State.CurrentUser, []catalog.Column{{Name: "v", Usage: usage, Nullable: true, NullabilityKnown: true}}, map[string]int{"v": 0})
	if err == nil {
		err = e.stageRowTypeLocked(stage, d)
	}
	if err == nil {
		err = stage.Create(d)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	v, err := sqltype.Integer(usage.Type, value)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.stageQueryTableRows(context.Background(), stage, d, [][]sqltype.Value{{v}}, ref); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage, d
}

func TestTransactionalSchemaPublication(t *testing.T) {
	for _, phase := range []string{"commit", "unrelated", "name_conflict", "epoch", "savepoint"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			ref := transactionBegin(t, owner)
			op := transactionOperation(t, owner)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.gate(&parser.CreateTable{}); err != nil {
				t.Fatal(err)
			}
			ended := false
			defer func() {
				scope.Finish(errors.New("discard publication unit"))
				op.Done()
				if !ended {
					transactionEnd(t, owner, ref, false)
				}
			}()
			before := e.CatalogRead()
			scope.owner.Mu.Lock()
			mark := captureSavepoint(scope.owner, "private_stage")
			scope.owner.Mu.Unlock()
			oldRoot := mark.Catalog
			stage, d := transactionalSchemaUnitStage(t, e, op, "publication_private", ref, 7)
			prepared, err := e.ValidateCatalogStage(op.Context(), stage)
			if err != nil {
				t.Fatal(err)
			}
			var unrelated *catalog.Relation
			if phase == "unrelated" || phase == "name_conflict" {
				name := "publication_unrelated"
				if phase == "name_conflict" {
					name = d.Name()
				}
				peer, done := roleOperation(t, e, "postgres")
				winner, w := transactionalSchemaUnitStage(t, e, peer, name, ids.TxRef{}, 19)
				publication, err := e.ValidateCatalogStage(peer.Context(), winner)
				if err == nil {
					_, err = e.PublishCatalog(publication, PublicationToken{Epoch: winner.Base.Epoch()})
				}
				done()
				if err != nil {
					t.Fatal(err)
				}
				unrelated = w
			}
			if phase == "epoch" {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if phase == "name_conflict" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Constraint != "pg_type_typname_nsp_index" {
					t.Fatal(err)
				}
				if e.CatalogRead().Get(d.Address()) != nil || e.storage.Relations[d.Address()] != nil {
					t.Fatal("failed stage published a partial table/store")
				}
				if e.CatalogRead().Get(unrelated.Address()) != unrelated {
					t.Fatal("name winner lost")
				}
				return
			}
			if phase == "epoch" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
				if e.CatalogRead().Get(d.Address()) != nil || e.storage.Relations[d.Address()] != nil {
					t.Fatal("old epoch resurrected private state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().Get(d.Address()) != nil || e.storage.Relations[d.Address()] != nil {
				t.Fatal("stage merge published globally")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			private := e.privateCatalogLocked(scope.owner.Catalog)
			old := e.privateCatalogLocked(oldRoot)
			stagePrivate := e.stageCatalogBaseLocked(stage)
			privateStore := e.privateStorageLocked(scope.owner.Catalog, d.Address())
			stageStore := e.stageStorageLocked(stage, d.Address())
			e.mu.Unlock()
			if private.Get(d.Address()) != d || old.Get(d.Address()) != nil || before.Get(d.Address()) != nil {
				t.Fatal("journal roots were mutated or private descriptor lost")
			}
			if stagePrivate.Get(d.Address()) != d || privateStore == nil || stageStore != privateStore {
				t.Fatal("ordinary stage validation lost explicit owner's private descriptor/store")
			}
			if phase == "savepoint" {
				for range 2 {
					scope.owner.Mu.Lock()
					restoreSavepoint(scope.owner, mark, mark.Settings.Clone())
					scope.owner.Mu.Unlock()
					e.locks.Restore(scope.ticket, mark.Locks)
				}
				e.mu.Lock()
				restored := e.privateCatalogLocked(scope.owner.Catalog)
				e.mu.Unlock()
				if restored.Get(d.Address()) != nil || len(scope.owner.Writes[d.Address()]) != 0 {
					t.Fatal("savepoint retained private descriptor/rows")
				}
				op.Done()
				transactionEnd(t, owner, ref, true)
				ended = true
				if e.CatalogRead().Get(d.Address()) != nil || e.storage.Relations[d.Address()] != nil {
					t.Fatal("savepoint-discarded graph resurrected at commit")
				}
				return
			}
			key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}
			created, ok := stage.NameBinding(key)
			if !ok || created != d.Address() || private.NameAddress(key) != created {
				t.Fatal("created result is not actual staged identity", created)
			}
			op.Done()
			transactionEnd(t, owner, ref, true)
			ended = true
			committed := e.CatalogRead()
			if committed.Get(d.Address()) != d || committed.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}).OwningRelation != d.Address() {
				t.Fatal("table/rowtype publication split")
			}
			store := e.storage.Relations[d.Address()]
			if store == nil || len(store.Versions) != 1 {
				t.Fatal("row/catalog publication split", store)
			}
			value, valueErr := store.Versions[0].Values[0].Integer()
			if valueErr != nil || value != 7 {
				t.Fatal(value, valueErr)
			}
			if unrelated != nil && committed.Get(unrelated.Address()) != unrelated {
				t.Fatal("unrelated commit overwritten")
			}
		})
	}
}
