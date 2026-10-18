package engine

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// All setup uses catalog/transaction functions, not a public SQL executor.
func partitionAttachUnitInstall(t *testing.T, e *Engine, relations ...*relationDescriptor) {
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
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func partitionAttachUnitRoot(t *testing.T, e *Engine, name string, id ids.OID) *relationDescriptor {
	t.Helper()
	d := inheritanceUnitRelation(e, name, id, "k")
	d.relationKind = partitionedRelation
	class, _ := sqltype.DefaultIndexClass(d.columns[0].Usage.Type)
	comparison, err := sqltype.BindIndexOrdering(d.columns[0].Usage, class)
	if err != nil {
		t.Fatal(err)
	}
	d.partition = &partitionDefinition{keys: []partitionKeyEntry{{attribute: 1, usage: d.columns[0].Usage, class: class, comparison: comparison, label: "k"}}}
	return d
}

func partitionAttachUnitStage(t *testing.T, e *Engine, catalog CatalogRead, parent, child *relationDescriptor) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(catalog)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	p, c := *parent, *child
	definition := *p.partition
	definition.children = append(append([]ids.ObjectAddress(nil), definition.children...), child.address)
	definition.defaultChild = child.address
	p.partition = &definition
	p.revision++
	c.membership = &partitionMembership{parent: p.address, defaultBound: true}
	c.revision++
	if err := stage.Replace(&p); err != nil {
		t.Fatal(err)
	}
	if err := stage.Replace(&c); err != nil {
		t.Fatal(err)
	}
	return stage
}

func partitionAttachUnitScope(t *testing.T, e *Engine, session *Session) (*StatementScope, *Operation) {
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
		if !scope.finished {
			scope.Finish(context.Canceled)
		}
		op.Done()
	})
	if err := scope.gate(&parser.Select{}); err != nil {
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
			child.relationKind = tableRelation
			partitionAttachUnitInstall(t, e, parent, child)
			parent = e.catalog.state.MustRelation(parent.address)
			child = e.catalog.state.MustRelation(child.address)
			stage := partitionAttachUnitStage(t, e, e.CatalogRead(), parent, child)
			store := e.storage[child.address]
			stage.partitionReads = []partitionReadFence{{address: child.address, storage: store, length: len(store.versions), ends: store.ends, generation: store.generation}}
			// A real captured type provider is an exact publication predecessor.
			providerAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
			stage.domainBases = append(stage.domainBases, e.catalog.state.Get(providerAddress))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "store":
				copy := *store
				e.storage[child.address] = &copy
			case "length":
				store.versions = append(store.versions, rowVersion{row: 1, version: 1})
			case "ends":
				store.ends = &deletionRoot{rows: map[ids.RowID]rowEnd{}}
			case "generation":
				store.generation++
			case "provider":
				typ := *e.catalog.state.MustType(providerAddress)
				typ.revision++
				next := cloneCatalog(e.catalog.state)
				next.descriptors[providerAddress] = &typ
				e.catalog.state = next
			case "cancel":
				cancel()
			}
			before := e.catalog.state
			stores, roots := maps.Clone(e.storage), maps.Clone(e.indexRoots)
			changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			if err == nil {
				t.Fatal("stale validation published an attachment")
			}
			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.catalog.state != before || !maps.Equal(e.storage, stores) || !maps.Equal(e.indexRoots, roots) || len(changes.Definitions) != 0 {
				t.Fatal("failed final veto installed partial catalog/storage/index roots")
			}
			if e.catalog.state.Get(parent.address) != parent || e.catalog.state.Get(child.address) != child || child.membership != nil || len(parent.partition.children) != 0 {
				t.Fatal("failed attachment changed original edges")
			}
		})
	}
}
