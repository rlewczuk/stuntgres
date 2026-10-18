package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestInheritanceAdmissionParentShareUpdateExclusive(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "p", 22000, "a")
	child := inheritanceUnitRelation(e, "c", 22001, "a")
	child.inheritance = &tableInheritance{parents: []ids.ObjectAddress{parent.address}}
	e.state = inheritanceUnitView(e, parent).state
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
	ref := e.domain.Ref(e.CatalogRead().Epoch(), parent.address)
	if err := holder.acquireSequence(ref, accessExclusive, true); err != nil {
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
	stage.descriptors[child.address] = child
	stage.captureLikeSource(parent)
	completed := make(chan error, 1)
	go func() { completed <- scope.acquireDefinitionProviders(stage) }()
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ref}]
	exact := entry != nil && len(entry.pending) == 1 && entry.pending[0].mode == shareUpdateExclusive && entry.pending[0].ticket == scope.ticket
	e.locks.mu.Unlock()
	cancel()
	lockAwait(t, completed, context.Canceled)
	if !exact {
		t.Fatal("parent admission did not queue exact SUE")
	}
	if e.state.descriptors[child.address] != nil || stage.likeSources[0] != parent {
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
	replacement.revision++
	for _, current := range []CatalogRead{inheritanceUnitView(e, &replacement), inheritanceUnitView(e)} {
		if err := stage.validateLikeSources(current); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("stale parent capture accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := e.state
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.state != before {
		t.Fatal("canceled validation changed catalog")
	}
}
