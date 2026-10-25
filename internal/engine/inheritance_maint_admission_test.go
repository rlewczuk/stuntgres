package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func inheritanceMaintPublished(t *testing.T, attached bool) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e, p, c := inheritanceDMLPublishedGraph(t)
	if !attached {
		next := *c
		next.Inheritance = nil
		next.ObjectRevision++
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		queryCreatePublish(t, e, stage)
		c = e.CatalogRead().MustRelation(c.Address())
	}
	return e, p, c
}

func TestInheritanceMaintAdmission(t *testing.T) {
	for _, attach := range []bool{true, false} {
		name, action, mode := "attach", "INHERIT", lock.ShareUpdateExclusive
		if !attach {
			name, action, mode = "detach", "NO INHERIT", lock.AccessShare
		}
		t.Run(name, func(t *testing.T) {
			e, p, c := inheritanceMaintPublished(t, !attach)
			holder := inheritanceAlterAXHolder(t, e, p)
			command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE c "+action+" p")
			if len(command.scope.Inheritance.Relationships) != 1 {
				t.Fatal("parent occurrence was not captured before child admission")
			}
			childRef := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), c.Address())
			parentRef := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
			if entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: childRef}]; entry == nil || entry.Holders[command.scope.LockTicket].Mode&lock.AccessExclusive == 0 {
				t.Fatal("child lacks actual AX grant")
			}
			before := e.CatalogRead()
			done := make(chan error, 1)
			joined := false
			go func() {
				done <- admitInheritanceRelationship(command.scope, &command.scope.Inheritance.Relationships[0])
			}()
			t.Cleanup(func() {
				command.cancel()
				holder.Finish(nil)
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("relationship admission worker did not join")
					}
				}
			})
			lockPending(t, e, 1)
			entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: parentRef}]
			if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Ticket != command.scope.LockTicket || entry.Pending[0].Mode != mode {
				t.Fatal("wrong actual parent request", entry)
			}
			command.cancel()
			var err error
			select {
			case err = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("canceled endpoint wait did not join")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("wrong canceled wait outcome", err)
			}
			command.scope.Finish(err)
			holder.Finish(nil)
			if e.CatalogRead().State() != before.State() || e.CatalogRead().Get(c.Address()) != c || e.CatalogRead().Get(p.Address()) != p {
				t.Fatal("canceled endpoint admission published state")
			}
			lockEmpty(t, e)
		})
	}
}

func TestInheritanceMaintPublication(t *testing.T) {
	e, p, c := inheritanceMaintPublished(t, false)
	command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE c INHERIT p")
	if err := admitInheritanceRelationship(command.scope, &command.scope.Inheritance.Relationships[0]); err != nil {
		t.Fatal(err)
	}
	before := e.CatalogRead()
	originalStore := e.Storage.Relations[c.Address()]
	if err := command.run(); err != nil {
		t.Fatal(err)
	}
	candidate, ok := command.scope.Catalog.Descriptors[c.Address()].(*catalog.Relation)
	if !ok || !candidate.Inheritance.HasParent(p.Address()) || e.CatalogRead().State() != before.State() || e.Storage.Relations[c.Address()] != originalStore {
		t.Fatal("candidate did not stay private and storage-preserving")
	}
	// A genuine catalog replacement while the unpublished candidate exists must
	// fail the exact grant-time endpoint fence, not retarget its name.
	replacement := *p
	replacement.ObjectRevision++
	replacement.Comment = "actual subsequent parent revision"
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	stable := e.CatalogRead()
	err := command.scope.Finish(nil)
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("late endpoint replacement escaped Finish fence", err)
	}
	if e.CatalogRead().State() != stable.State() || e.CatalogRead().Get(c.Address()) != c || e.Storage.Relations[c.Address()] != originalStore {
		t.Fatal("late veto published candidate edge/storage")
	}
	lockEmpty(t, e)
}

func TestInheritanceMaintEndpointReplacement(t *testing.T) {
	for _, attach := range []bool{true, false} {
		action := "INHERIT"
		if !attach {
			action = "NO INHERIT"
		}
		t.Run(action, func(t *testing.T) {
			e, p, c := inheritanceMaintPublished(t, !attach)
			holder := inheritanceAlterAXHolder(t, e, p)
			command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE c "+action+" p")
			done := make(chan error, 1)
			joined := false
			go func() {
				done <- admitInheritanceRelationship(command.scope, &command.scope.Inheritance.Relationships[0])
			}()
			t.Cleanup(func() {
				command.cancel()
				holder.Finish(nil)
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("endpoint replacement worker did not join")
					}
				}
			})
			lockPending(t, e, 1)
			replacement := *p
			replacement.ObjectRevision++
			replacement.Comment = "parent changed while its admission was queued"
			stage := e.BeginCatalogStage(e.CatalogRead())
			if err := stage.Replace(&replacement); err != nil {
				t.Fatal(err)
			}
			queryCreatePublish(t, e, stage)
			stable := e.CatalogRead()
			holder.Finish(nil)
			var err error
			select {
			case err = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("endpoint replacement admission did not join")
			}
			if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("queued endpoint definition was silently adopted", err)
			}
			command.scope.Finish(err)
			if e.CatalogRead().State() != stable.State() || e.CatalogRead().Get(c.Address()) != c {
				t.Fatal("failed endpoint admission published a relationship")
			}
			lockEmpty(t, e)
		})
	}
}

func TestInheritanceMaintDropMultiplicity(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "inherited"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			e, p, c := inheritanceDMLPublishedGraph(t)
			if local {
				next := *c
				next.Inheritance = catalog.CloneInheritance(c.Inheritance)
				fact := next.Inheritance.Columns[next.Attributes[0]]
				fact.Local = true
				next.Inheritance.Columns[next.Attributes[0]] = fact
				next.ObjectRevision++
				stage := e.BeginCatalogStage(e.CatalogRead())
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, stage)
			}
			for _, only := range []bool{false, true} {
				raw := &parser.AlterTable{Only: only, Actions: []parser.AlterTableAction{{Action: parser.DropColumn, Column: parser.Identifier{Text: p.Columns[0].Name}}}}
				plan, _, err := table.PlanInheritanceAlter(e.CatalogRead(), p, raw)
				if err != nil {
					t.Fatal(err)
				}
				target := p.Address()
				target.SubID = p.Attributes[0]
				if got, want := plan.ColumnDropMultiple(e.CatalogRead(), target), !local && !only; got != want {
					t.Fatal("origin decrement was confused with physical deletion", local, only, got, want)
				}
			}
		})
	}
}

func TestInheritanceMaintReviewAbsentEndpoint(t *testing.T) {
	e, _, child := inheritanceMaintPublished(t, false)
	holder := inheritanceAlterAXHolder(t, e, child)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	op, doneOperation, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(doneOperation)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	raw := statement(t, "ALTER TABLE c INHERIT appeared").(*parser.AlterTable)
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	joined := false
	go func() { done <- scope.AcquireRelation(raw, true) }()
	t.Cleanup(func() {
		cancel()
		holder.Finish(nil)
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("absent endpoint child-admission worker did not join")
			}
		}
	})
	lockPending(t, e, 1)
	if len(scope.Inheritance.Relationships) != 1 || scope.Inheritance.Relationships[0].Lexical.Domain() != nil || scope.Inheritance.Relationships[0].Deferred == nil || scope.Inheritance.Relationships[0].Admitted {
		t.Fatal("missing endpoint was not retained as a deferred absent observation")
	}
	stage, _ := queryCreateStage(t, e, "appeared", 1)
	queryCreatePublish(t, e, stage)
	stable := e.CatalogRead()
	holder.Finish(nil)
	select {
	case err = <-done:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("child admission did not join")
	}
	if err != nil {
		t.Fatal("parent semantics escaped into child admission", err)
	}
	err = admitInheritanceRelationship(scope, &scope.Inheritance.Relationships[0])
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("initially absent spelling retargeted after real AX wait", err)
	}
	if final := scope.Finish(err); !errors.Is(final, ids.ErrCatalogReplaced) {
		t.Fatal("absent endpoint veto was lost during Finish", final)
	}
	if e.CatalogRead().State() != stable.State() || e.CatalogRead().Get(child.Address()) != child {
		t.Fatal("absent endpoint failure published an edge or discarded concurrent work")
	}
	lockEmpty(t, e)
}

func TestInheritanceMaintReviewMarkerAdmission(t *testing.T) {
	e := New()
	parentStage, parent := queryCreateStage(t, e, "p", 1)
	queryCreatePublish(t, e, parentStage)
	childStage, child := queryCreateStage(t, e, "c", 1)
	queryCreatePublish(t, e, childStage)
	if parent.History.HasSubclasses {
		t.Fatal("fixture starts after its first-child publication")
	}
	holder := inheritanceAlterAXHolder(t, e, parent)
	command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE c INHERIT p")
	done := make(chan error, 1)
	joined := false
	go func() { done <- command.run() }()
	t.Cleanup(func() {
		command.cancel()
		holder.Finish(nil)
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("marker admission worker did not join")
			}
		}
	})
	lockPending(t, e, 1)
	if command.scope.Inheritance.Alter == nil || command.scope.Inheritance.Alter.Stage.Base.Get(parent.Address()) != parent {
		t.Fatal("parent wait did not occur inside the actual older-base stage")
	}
	siblingStage, sibling := queryCreateStage(t, e, "first_child", 1)
	sibling.Inheritance = &catalog.TableInheritance{
		Parents: inheritanceUnitParents(parent.Address()),
		Columns: map[int32]catalog.InheritedColumn{sibling.Attributes[0]: {Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: parent.Attributes[0]}}}},
		NotNull: map[int32]catalog.InheritedConstraint{},
		Checks:  map[string]catalog.InheritedConstraint{},
	}
	queryCreatePublish(t, e, siblingStage)
	marker := e.CatalogRead().MustRelation(parent.Address())
	if marker == parent || !table.InheritanceMarkerPublication(parent, marker) {
		t.Fatal("fixture did not publish the genuine first-child marker")
	}
	holder.Finish(nil)
	var err error
	select {
	case err = <-done:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("marker admission did not join")
	}
	if err != nil {
		t.Fatal("genuine intervening marker rejected", err)
	}
	if err := command.scope.Finish(nil); err != nil {
		t.Fatal("marker reconciliation failed at actual publication", err)
	}
	current := e.CatalogRead()
	if current.Get(parent.Address()) != marker || !current.MustRelation(child.Address()).Inheritance.HasParent(parent.Address()) || current.Get(sibling.Address()) != sibling {
		t.Fatal("publication reminted the marker, lost an edge or discarded concurrent work")
	}
	if parent.History.HasSubclasses || child.Inheritance != nil {
		t.Fatal("marker reconciliation mutated old source descriptors")
	}
	lockEmpty(t, e)
}
