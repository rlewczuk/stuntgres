package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
)

func TestTransactionalDefinitionDependencyFences(t *testing.T) {
	t.Run("outgoing_committed_providers", func(t *testing.T) {
		e := New()
		owner := sessionStart(t, e, nil)
		ref := transactionBegin(t, owner)
		defer transactionEnd(t, owner, ref, false)
		committed := e.CatalogRead()
		definitionUnitGraph(t, e, owner)
		for _, address := range []ids.ObjectAddress{{ClassID: ids.ProcedureClassID, ObjectID: 330}, {ClassID: ids.ProcedureClassID, ObjectID: 351}, {ClassID: ids.TypeClassID, ObjectID: 23}} {
			if owner.Transactions.Catalog.Originals[address] != committed.Get(address) {
				t.Errorf("journal failed to retain consumed committed provider %v", address)
			}
		}
		proc, _ := committed.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 330})
		changed := *proc
		changed.ObjectRevision++
		stage := e.BeginCatalogStage(committed)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(&changed); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err == nil {
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: committed.Epoch()})
		}
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		err = e.catalogPreflightLocked(owner.Transactions.Catalog)
		e.mu.Unlock()
		var conflict catalog.Conflict
		if !errors.As(err, &conflict) {
			t.Fatal("stale exact handler predecessor admitted", err)
		}
	})
	t.Run("incoming_definition_edges", func(t *testing.T) {
		e := New()
		owner := sessionStart(t, e, nil)
		definitionUnitGraph(t, e, owner)
		base := e.CatalogRead()
		method, _ := base.LookupAccessMethod("unit_definition_am")
		class, _ := definitionUnitClass(t, base)
		family, _ := base.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: class.Family})
		support, _ := base.FamilySupport(class.Family, 23, 23, 1)
		op := &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: 99011}, ObjectRevision: 1, Family: family.Address().ObjectID, Left: 23, Right: 23, Operator: 97, Method: method.Address().ObjectID, Number: 1, Purpose: 's'}
		for _, tc := range []struct {
			name       string
			descriptor catalog.Descriptor
			target     ids.ObjectAddress
		}{
			{"family_method", family, method.Address()},
			{"class_method", class, method.Address()},
			{"class_family", class, family.Address()},
			{"support_family", support, family.Address()},
			{"support_procedure", support, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 351}},
			{"support_type", support, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}},
			{"operator_family", op, family.Address()},
			{"operator_implementation", op, ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 97}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// A changed committed incoming edge appeared after retirement capture.
				original := *base.State()
				original.RemoveDescriptor(tc.descriptor.Address())
				current := *base.State()
				current.SetDescriptor(tc.descriptor.Address(), tc.descriptor)
				candidate := current
				candidate.RemoveDescriptor(tc.target)
				observations := []session.DependencyObservation{{Base: &original, Targets: []ids.ObjectAddress{tc.target}}}
				var conflict catalog.Conflict
				if err := catalogDependencyPreflight(&current, &candidate, observations, nil); !errors.As(err, &conflict) {
					t.Fatal("retirement admitted new dangling incoming edge", err)
				}
				candidate.RemoveDescriptor(tc.descriptor.Address())
				if err := catalogDependencyPreflight(&current, &candidate, observations, nil); err != nil {
					t.Fatal("removed candidate edge impersonated survivor", err)
				}
			})
		}
	})
}

func TestTransactionalDefinitionReplacementFences(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	ref := transactionBegin(t, owner)
	definitionUnitGraph(t, e, owner)
	original := definitionUnitRead(e, owner)
	class, bound := definitionUnitClass(t, original)
	support, err := routine.BindClassSupport(original, bound)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = e.preparePrivateCatalog(context.Background(), owner.Transactions.Catalog, ref.Epoch())
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("replacement accepted private old-epoch graph", err)
	}
	if e.CatalogRead().Get(class.Address()) != nil {
		t.Fatal("replacement resurrected old class")
	}
	if original.Get(class.Address()) != class || support.Support(1).Routine != 351 {
		t.Fatal("replacement mutated retained descriptor/support")
	}
	transactionEnd(t, owner, ref, false)
}
