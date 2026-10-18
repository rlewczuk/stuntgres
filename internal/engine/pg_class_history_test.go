package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func pgClassHistoryCatalog(e *Engine) (CatalogRead, *relationDescriptor) {
	state := cloneCatalog(e.CatalogRead().state)
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200100}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: "history_parent", revision: 1, owner: e.authorizationDomain.Principal(10)}
	state.setDescriptor(parent.address, parent)
	state.bindName(descriptorNameKey(parent), parent.address)
	return e.catalog.read(state), parent
}

func TestPgClassSubclassPublication(t *testing.T) {
	for _, edge := range []string{"inheritance", "table_partition", "index_partition", "ordinary_index"} {
		t.Run(edge, func(t *testing.T) {
			e := New()
			base, parent := pgClassHistoryCatalog(e)
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			child := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, namespace: parent.namespace, name: "history_child", revision: 1, owner: parent.owner}
			switch edge {
			case "inheritance":
				child.inheritance = &tableInheritance{parents: []ids.ObjectAddress{parent.address}}
			case "table_partition":
				child.membership = &partitionMembership{parent: parent.address}
			case "index_partition":
				child.sqlIndex = &indexDefinition{partitionParent: parent.address}
			case "ordinary_index":
				child.sqlIndex = &indexDefinition{parent: parent.address}
			}
			if err := stage.Create(child); err != nil {
				t.Fatal(err)
			}
			next := *parent
			next.name = "history_renamed"
			next.revision = 2
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			err := e.markStageSubclassHistoryLocked(stage)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got := stage.descriptors[parent.address].(*relationDescriptor)
			want := edge != "ordinary_index"
			if got.hasSubclasses != want || got.name != next.name || got.revision != next.revision || parent.hasSubclasses || next.hasSubclasses {
				t.Fatal("candidate/source immutability", got, parent, &next)
			}
			if base.Get(parent.address) != parent {
				t.Fatal("private candidate leaked")
			}
			// Discarding or failing candidate admission cannot publish history.
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := e.ValidateCatalogStage(canceled, stage); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if parent.hasSubclasses {
				t.Fatal("canceled candidate mutated base")
			}
		})
	}
	// Parent and child created together retain their staged revision and rowtype.
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200110}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: "same_stage", revision: 7, rowType: 200112, owner: e.authorizationDomain.Principal(10)}
	child := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200111}, namespace: parent.namespace, name: "same_child", revision: 8, owner: parent.owner, inheritance: &tableInheritance{parents: []ids.ObjectAddress{parent.address}}}
	if err := stage.Create(parent); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(child); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err := e.markStageSubclassHistoryLocked(stage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got := stage.descriptors[parent.address].(*relationDescriptor)
	if !got.hasSubclasses || got.revision != 7 || got.rowType != 200112 || parent.hasSubclasses {
		t.Fatal("same-stage identity/history", got)
	}
}

func TestPgClassSubclassReplacement(t *testing.T) {
	e := New()
	catalog, parent := pgClassHistoryCatalog(e)
	child := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, inheritance: &tableInheritance{parents: []ids.ObjectAddress{parent.address}}}
	catalog.state.setDescriptor(child.address, child)
	history := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200102}, hasSubclasses: true, owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}
	catalog.state.setDescriptor(history.address, history)
	if err := normalizeReplacementSubclassHistory(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	got := catalog.MustRelation(parent.address)
	if !got.hasSubclasses || got == parent || parent.hasSubclasses || !history.hasSubclasses {
		t.Fatal("normalization mutated retained roots", got, parent)
	}
	base := e.catalog.read(cloneCatalog(catalog.state))
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	cleared := *got
	cleared.hasSubclasses = false
	if err := stage.Replace(&cleared); err == nil {
		t.Fatal("same-identity replacement cleared lazy history")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalizeReplacementSubclassHistory(canceled, catalog); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	missing := e.catalog.read(cloneCatalog(catalog.state))
	missing.state.removeDescriptor(parent.address)
	if err := normalizeReplacementSubclassHistory(context.Background(), missing); err == nil {
		t.Fatal("dangling edge accepted")
	}
}
