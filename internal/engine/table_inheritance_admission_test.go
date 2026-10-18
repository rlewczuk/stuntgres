package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"testing"
)

func TestInheritanceAdmissionParentShareUpdateExclusive(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "p", 22000, "a")
	child := inheritanceUnitRelation(e, "c", 22001, "a")
	child.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{parent.Address()}}
	e.catalog.state = inheritanceUnitView(e, parent).State()
	holderOp, holderDone := roleOperation(t, e, "postgres")
	defer holderDone()
	holder, err := e.StartStatement(holderOp)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Finish(errors.New("discard unit holder"))
	if err := holder.gate(&parser.CreateTable{}); err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), parent.Address())
	if err := holder.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
		t.Fatal(err)
	}
	session := sessionStart(t, e, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("discard unit creator"))
	if err := scope.gate(&parser.CreateTable{}); err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.Descriptors[child.Address()] = child
	stage.captureLikeSource(parent)
	completed := make(chan error, 1)
	go func() { completed <- scope.acquireDefinitionProviders(stage) }()
	lockPending(t, e, 1)
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	exact := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.ShareUpdateExclusive && entry.Pending[0].Ticket == scope.ticket
	cancel()
	lockAwait(t, completed, context.Canceled)
	if !exact {
		t.Fatal("parent admission did not queue exact SUE")
	}
	if e.catalog.state.Get(child.Address()) != nil || stage.likeSources[0] != parent {
		t.Fatal("canceled admission published or changed parent capture")
	}
}
func TestInheritanceAdmissionCapturedParentFence(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "p", 22000, "a")
	base := inheritanceUnitView(e, parent)
	stage := e.BeginCatalogStage(base)
	stage.captureLikeSource(parent)
	if err := stage.validateLikeSources(base); err != nil {
		t.Fatal(err)
	}
	replacement := *parent
	replacement.ObjectRevision++
	for _, current := range []catalog.Read{inheritanceUnitView(e, &replacement), inheritanceUnitView(e)} {
		if err := stage.validateLikeSources(current); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("stale parent capture accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := e.catalog.state
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.catalog.state != before {
		t.Fatal("canceled validation changed catalog")
	}
}
