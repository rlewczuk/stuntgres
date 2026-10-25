package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func pgClassHistoryCatalog(e *Engine) (catalog.Read, *catalog.Relation) {
	state := catalog.CloneState(e.CatalogRead().State())
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200100}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "history_parent", ObjectRevision: 1, Owner: e.RoleStore.Domain.Principal(10)}
	state.SetDescriptor(parent.Address(), parent)
	state.BindName(catalog.DescriptorNameKey(parent), parent.Address())
	return e.Catalog.Read(state), parent
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
				child.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(parent.Address())}
			case "table_partition":
				child.Membership = &catalog.PartitionMembership{Parent: parent.Address()}
			case "index_partition":
				child.SQLIndex = &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, PartitionParent: parent.Address()}
			case "ordinary_index":
				child.SQLIndex = &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address()}
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
			e.Mu.Lock()
			err := stage.MarkSubclassHistoryLocked(e.Catalog.RevisionLocked, stage.Replace)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got := stage.Get(parent.Address()).(*catalog.Relation)
			want := edge != "ordinary_index"
			if got.History.HasSubclasses != want || got.Name() != next.Name() || got.Revision() != next.Revision() || parent.History.HasSubclasses || next.History.HasSubclasses {
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
			if parent.History.HasSubclasses {
				t.Fatal("canceled candidate mutated base")
			}
		})
	}
	// Parent and child created together retain their staged revision and rowtype.
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200110}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "same_stage", ObjectRevision: 7, RowType: 200112, Owner: e.RoleStore.Domain.Principal(10)}
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200111}, ObjectNamespace: parent.Namespace(), ObjectName: "same_child", ObjectRevision: 8, Owner: parent.Owner, Inheritance: &catalog.TableInheritance{Parents: inheritanceUnitParents(parent.Address())}}
	if err := stage.Create(parent); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(child); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err := stage.MarkSubclassHistoryLocked(e.Catalog.RevisionLocked, stage.Replace)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got := stage.Get(parent.Address()).(*catalog.Relation)
	if !got.History.HasSubclasses || got.Revision() != 7 || got.RowType != 200112 || parent.History.HasSubclasses {
		t.Fatal("same-stage identity/history", got)
	}
}

func TestPgClassSubclassReplacement(t *testing.T) {
	e := New()
	view, parent := pgClassHistoryCatalog(e)
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200101}, Inheritance: &catalog.TableInheritance{Parents: inheritanceUnitParents(parent.Address())}}
	view.State().SetDescriptor(child.Address(), child)
	history := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200102}, History: catalog.PublicationHistory{HasSubclasses: true}, Owner: e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)}
	view.State().SetDescriptor(history.Address(), history)
	if err := catalog.NormalizeReplacementSubclassHistory(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	got := view.MustRelation(parent.Address())
	if !got.History.HasSubclasses || got == parent || parent.History.HasSubclasses || !history.History.HasSubclasses {
		t.Fatal("normalization mutated retained roots", got, parent)
	}
	base := e.Catalog.Read(catalog.CloneState(view.State()))
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	cleared := *got
	cleared.History.HasSubclasses = false
	if err := stage.Replace(&cleared); err == nil {
		t.Fatal("same-identity replacement cleared lazy history")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := catalog.NormalizeReplacementSubclassHistory(canceled, view); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	missing := e.Catalog.Read(catalog.CloneState(view.State()))
	missing.State().RemoveDescriptor(parent.Address())
	if err := catalog.NormalizeReplacementSubclassHistory(context.Background(), missing); err == nil {
		t.Fatal("dangling edge accepted")
	}
}
