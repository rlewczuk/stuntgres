package engine

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// All setup uses catalog/transaction functions, not a public SQL executor.
func partitionAttachUnitInstall(t *testing.T, e *Engine, relations ...*catalog.Relation) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	for _, d := range relations {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func partitionAttachUnitRoot(t *testing.T, e *Engine, name string, id ids.OID) *catalog.Relation {
	t.Helper()
	d := inheritanceUnitRelation(e, name, id, "k")
	d.RelationKind = catalog.PartitionedRelation
	class, _ := sqltype.DefaultIndexClass(d.Columns[0].Usage.Type)
	comparison, err := operatorFamilyTestOrdering(d.Columns[0].Usage, class)
	if err != nil {
		t.Fatal(err)
	}
	d.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: d.Columns[0].Usage, Class: class, Comparison: comparison, Label: "k"}}}
	return d
}

func partitionAttachUnitStage(t *testing.T, e *Engine, view catalog.Read, parent, child *catalog.Relation) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(view)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	p, c := *parent, *child
	definition := *p.Partition
	definition.Children = append(append([]ids.ObjectAddress(nil), definition.Children...), child.Address())
	definition.DefaultChild = child.Address()
	p.Partition = &definition
	p.ObjectRevision++
	c.Membership = &catalog.PartitionMembership{Parent: p.Address(), DefaultBound: true}
	c.ObjectRevision++
	if err := stage.Replace(&p); err != nil {
		t.Fatal(err)
	}
	if err := stage.Replace(&c); err != nil {
		t.Fatal(err)
	}
	return stage
}

func partitionAttachUnitScope(t *testing.T, e *Engine, session *session.Session) (*StatementScope, *session.Operation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	op, err := session.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if !scope.Finished {
			scope.Finish(context.Canceled)
		}
		op.Done()
	})
	if err := scope.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	if err := scope.snapshotPhase(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	return scope, op
}

func TestPartitionAttachPublicationFences(t *testing.T) {
	for _, change := range []string{"store", "length", "ends", "generation", "provider", "cancel"} {
		t.Run(change, func(t *testing.T) {
			e := New()
			parent := partitionAttachUnitRoot(t, e, "attach_fence_parent", 46000)
			child := inheritanceUnitRelation(e, "attach_fence_child", 46001, "k")
			child.RelationKind = catalog.TableRelation
			partitionAttachUnitInstall(t, e, parent, child)
			parent = e.Catalog.State.MustRelation(parent.Address())
			child = e.Catalog.State.MustRelation(child.Address())
			stage := partitionAttachUnitStage(t, e, e.CatalogRead(), parent, child)
			store := e.Storage.Relations[child.Address()]
			stage.StorageReads = []session.StorageReadFence{session.NewStorageReadFence(child.Address(), store)}
			// A real captured type provider is an exact publication predecessor.
			providerAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
			stage.DomainBases = append(stage.DomainBases, e.Catalog.State.Get(providerAddress))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "store":
				copy := *store
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(child.Address(), &copy)
				e.Mu.Unlock()
			case "length":
				store.Versions = append(store.Versions, storage.RowVersion{Row: 1, Version: 1})
			case "ends":
				store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{}}
			case "generation":
				store.Generation++
			case "provider":
				typ := *e.Catalog.State.MustType(providerAddress)
				typ.ObjectRevision++
				next := catalog.CloneState(e.Catalog.State)
				next.SetDescriptor(providerAddress, &typ)
				e.Catalog.PublishLocked(next)
			case "cancel":
				cancel()
			}
			before := e.Catalog.State
			stores, roots := maps.Clone(e.Storage.Relations), maps.Clone(e.Storage.IndexRoots)
			changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil {
				t.Fatal("stale validation published an attachment")
			}
			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Catalog.State != before || !maps.Equal(e.Storage.Relations, stores) || !maps.Equal(e.Storage.IndexRoots, roots) || len(changes.Definitions) != 0 {
				t.Fatal("failed final veto installed partial catalog/storage/index roots")
			}
			if e.Catalog.State.Get(parent.Address()) != parent || e.Catalog.State.Get(child.Address()) != child || child.Membership != nil || len(parent.Partition.Children) != 0 {
				t.Fatal("failed attachment changed original edges")
			}
		})
	}
}
