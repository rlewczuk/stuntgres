package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

func pgrowlocksUnitScope(t *testing.T, e *Engine, s *session.Session) *StatementScope {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	scope.Owner.Mu.Lock()
	ScopeAttachTransactionLocked(scope)
	scope.Owner.Mu.Unlock()
	SetScopeWorked(scope, true)
	t.Cleanup(func() { scope.Finish(context.Canceled); op.Done() })
	return scope
}
func TestPgrowlocksRelationIdentity(t *testing.T) {
	t.Run("function_only_and_prior_select_contributions", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		pgrowlocksUnitInstalled(t, e)
		n := pgrowlocksUnitExpression(t, e)
		reader, ticket := blockingUnitOwner(t, e)
		scope := pgrowlocksUnitScope(t, e, reader)
		scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
		resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
		for _, prior := range []bool{false, true} {
			if prior {
				request, err := e.Locks.StartAcquire(context.Background(), ticket, resource, lock.AccessShare, lock.Policy{})
				if err != nil || request != nil {
					t.Fatal(request, err)
				}
			}
			rows, err := scope.WriteHost().Pgrowlocks("typed", n.Usage.Type)
			if err != nil || len(rows) != 0 {
				t.Fatal(rows, err)
			}
			if e.Locks.HasAccessShare(ticket, resource) != prior {
				t.Fatal("function removed earlier SELECT or retained its added AS", prior)
			}
		}
	})
	t.Run("exact_queued_contribution_cancel_and_epoch", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		_, holder := blockingUnitOwner(t, e)
		_, waiter := blockingUnitOwner(t, e)
		resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
		request, err := e.Locks.StartAcquire(context.Background(), holder, resource, lock.AccessExclusive, lock.Policy{})
		if err != nil || request != nil {
			t.Fatal(request, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		queued, err := e.Locks.StartAcquire(ctx, waiter, resource, lock.AccessShare, lock.Policy{})
		if err != nil || queued == nil {
			cancel()
			t.Fatal(queued, err)
		}
		cancel()
		if err := queued.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		e.Locks.ReleaseAddedAccessShare(waiter, resource, false)
		if e.Locks.HasAccessShare(waiter, resource) {
			t.Fatal("canceled request left function grant")
		}
		if err := Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		request, err = e.Locks.StartAcquire(context.Background(), waiter, resource, lock.AccessShare, lock.Policy{})
		if request != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("old identity admitted after replacement", request, err)
		}
	})
	t.Run("descriptor_readmission_never_retargets", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		reader, _ := blockingUnitOwner(t, e)
		scope := pgrowlocksUnitScope(t, e, reader)
		ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: "typed"}}}
		stored := e.Storage.Relations[d.Address()]
		e.Mu.Lock()
		auth, err := ScopeWriteAuthorizationLocked(scope, e)
		if err == nil {
			err = ScopePgrowlocksReadmitLocked(scope, e, auth, name, ref, d, stored)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		next := *d
		next.ObjectRevision++
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		domainUnitPublish(t, e, stage)
		e.Mu.Lock()
		auth, err = ScopeWriteAuthorizationLocked(scope, e)
		if err == nil {
			err = ScopePgrowlocksReadmitLocked(scope, e, auth, name, ref, d, stored)
		}
		e.Mu.Unlock()
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("descriptor changed during admission", err)
		}
	})
}
