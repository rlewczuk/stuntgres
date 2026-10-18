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
	parent := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200100}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "history_parent", ObjectRevision: 1, Owner: e.authorizationDomain.Principal(10)}
	state.setDescriptor(parent.Address(), parent)
	state.bindName(descriptorNameKey(parent), parent.Address())
	return e.catalog.read(state), parent
}

func TestPgClassSubclassPublication(t *testing.T) {
	for _, edge := range []string{"inheritance", "table_partition", "index_partition", "ordinary_index"} {
		t.Run(edge, func(t *testing.T) {
			e := New()
			base, parent := pgClassHistoryCatalog(e)
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			child := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, ObjectNamespace: parent.Namespace(), ObjectName: "history_child", ObjectRevision: 1, Owner: parent.Owner}
			switch edge {
			case "inheritance":
				child.Inheritance = &tableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}
			case "table_partition":
				child.Membership = &partitionMembership{Parent: parent.Address()}
			case "index_partition":
				child.SQLIndex = &indexDefinition{PartitionParent: parent.Address()}
			case "ordinary_index":
				child.SQLIndex = &indexDefinition{Parent: parent.Address()}
			}
			if err := stage.Create(child); err != nil {
				t.Fatal(err)
			}
			next := *parent
			next.ObjectName = "history_renamed"
			next.ObjectRevision = 2
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			err := e.markStageSubclassHistoryLocked(stage)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got := stage.descriptors[parent.Address()].(*relationDescriptor)
			want := edge != "ordinary_index"
			if got.HasSubclasses != want || got.Name() != next.Name() || got.Revision() != next.Revision() || parent.HasSubclasses || next.HasSubclasses {
				t.Fatal("candidate/source immutability", got, parent, &next)
			}
			if base.Get(parent.Address()) != parent {
				t.Fatal("private candidate leaked")
			}
			// Discarding or failing candidate admission cannot publish history.
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := e.ValidateCatalogStage(canceled, stage); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if parent.HasSubclasses {
				t.Fatal("canceled candidate mutated base")
			}
		})
	}
	// Parent and child created together retain their staged revision and rowtype.
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	parent := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200110}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "same_stage", ObjectRevision: 7, RowType: 200112, Owner: e.authorizationDomain.Principal(10)}
	child := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200111}, ObjectNamespace: parent.Namespace(), ObjectName: "same_child", ObjectRevision: 8, Owner: parent.Owner, Inheritance: &tableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}}
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
	got := stage.descriptors[parent.Address()].(*relationDescriptor)
	if !got.HasSubclasses || got.Revision() != 7 || got.RowType != 200112 || parent.HasSubclasses {
		t.Fatal("same-stage identity/history", got)
	}
}

func TestPgClassSubclassReplacement(t *testing.T) {
	e := New()
	catalog, parent := pgClassHistoryCatalog(e)
	child := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, Inheritance: &tableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}}
	catalog.state.setDescriptor(child.Address(), child)
	history := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200102}, HasSubclasses: true, Owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}
	catalog.state.setDescriptor(history.Address(), history)
	if err := normalizeReplacementSubclassHistory(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	got := catalog.MustRelation(parent.Address())
	if !got.HasSubclasses || got == parent || parent.HasSubclasses || !history.HasSubclasses {
		t.Fatal("normalization mutated retained roots", got, parent)
	}
	base := e.catalog.read(cloneCatalog(catalog.state))
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	cleared := *got
	cleared.HasSubclasses = false
	if err := stage.Replace(&cleared); err == nil {
		t.Fatal("same-identity replacement cleared lazy history")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalizeReplacementSubclassHistory(canceled, catalog); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	missing := e.catalog.read(cloneCatalog(catalog.state))
	missing.state.removeDescriptor(parent.Address())
	if err := normalizeReplacementSubclassHistory(context.Background(), missing); err == nil {
		t.Fatal("dangling edge accepted")
	}
}
