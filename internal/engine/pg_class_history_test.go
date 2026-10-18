package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func pgClassHistoryCatalog(e *Engine) (CatalogRead, *catalog.Relation) {
	state := catalog.CloneState(e.CatalogRead().State())
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200100}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "history_parent", ObjectRevision: 1, Owner: e.authorizationDomain.Principal(10)}
	state.SetDescriptor(parent.Address(), parent)
	state.BindName(catalog.DescriptorNameKey(parent), parent.Address())
	return e.catalog.read(state), parent
}

func TestPgClassSubclassPublication(t *testing.T) {
	for _, edge := range []string{"inheritance", "table_partition", "index_partition", "ordinary_index"} {
		t.Run(edge, func(t *testing.T) {
			e := New()
			base, parent := pgClassHistoryCatalog(e)
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, ObjectNamespace: parent.Namespace(), ObjectName: "history_child", ObjectRevision: 1, Owner: parent.Owner}
			switch edge {
			case "inheritance":
				child.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}
			case "table_partition":
				child.Membership = &catalog.PartitionMembership{Parent: parent.Address()}
			case "index_partition":
				child.SQLIndex = &catalog.IndexDefinition{PartitionParent: parent.Address()}
			case "ordinary_index":
				child.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address()}
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
			got := stage.Descriptors[parent.Address()].(*catalog.Relation)
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
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200110}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "same_stage", ObjectRevision: 7, RowType: 200112, Owner: e.authorizationDomain.Principal(10)}
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200111}, ObjectNamespace: parent.Namespace(), ObjectName: "same_child", ObjectRevision: 8, Owner: parent.Owner, Inheritance: &catalog.TableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}}
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
	got := stage.Descriptors[parent.Address()].(*catalog.Relation)
	if !got.HasSubclasses || got.Revision() != 7 || got.RowType != 200112 || parent.HasSubclasses {
		t.Fatal("same-stage identity/history", got)
	}
}

func TestPgClassSubclassReplacement(t *testing.T) {
	e := New()
	view, parent := pgClassHistoryCatalog(e)
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, Inheritance: &catalog.TableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}}
	view.State().SetDescriptor(child.Address(), child)
	history := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200102}, HasSubclasses: true, Owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}
	view.State().SetDescriptor(history.Address(), history)
	if err := normalizeReplacementSubclassHistory(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	got := view.MustRelation(parent.Address())
	if !got.HasSubclasses || got == parent || parent.HasSubclasses || !history.HasSubclasses {
		t.Fatal("normalization mutated retained roots", got, parent)
	}
	base := e.catalog.read(catalog.CloneState(view.State()))
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	cleared := *got
	cleared.HasSubclasses = false
	if err := stage.Replace(&cleared); err == nil {
		t.Fatal("same-identity replacement cleared lazy history")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalizeReplacementSubclassHistory(canceled, view); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	missing := e.catalog.read(catalog.CloneState(view.State()))
	missing.State().RemoveDescriptor(parent.Address())
	if err := normalizeReplacementSubclassHistory(context.Background(), missing); err == nil {
		t.Fatal("dangling edge accepted")
	}
}
