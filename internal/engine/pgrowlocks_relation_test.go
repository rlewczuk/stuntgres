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
	scope.owner.Mu.Lock()
	scope.attachTransactionLocked()
	scope.owner.Mu.Unlock()
	scope.worked = true
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
		scope.routineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
		resource := lock.Resource{ObjectRef: e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())}
		for _, prior := range []bool{false, true} {
			if prior {
				request, err := e.locks.StartAcquire(context.Background(), ticket, resource, lock.AccessShare, lock.Policy{})
				if err != nil || request != nil {
					t.Fatal(request, err)
				}
			}
			rows, err := (*statementHost)(scope).Pgrowlocks("typed", n.Usage.Type)
			if err != nil || len(rows) != 0 {
				t.Fatal(rows, err)
			}
			if e.locks.HasAccessShare(ticket, resource) != prior {
				t.Fatal("function removed earlier SELECT or retained its added AS", prior)
			}
		}
	})
	t.Run("exact_queued_contribution_cancel_and_epoch", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		_, holder := blockingUnitOwner(t, e)
		_, waiter := blockingUnitOwner(t, e)
		resource := lock.Resource{ObjectRef: e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())}
		request, err := e.locks.StartAcquire(context.Background(), holder, resource, lock.AccessExclusive, lock.Policy{})
		if err != nil || request != nil {
			t.Fatal(request, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		queued, err := e.locks.StartAcquire(ctx, waiter, resource, lock.AccessShare, lock.Policy{})
		if err != nil || queued == nil {
			cancel()
			t.Fatal(queued, err)
		}
		cancel()
		if err := queued.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		e.locks.ReleaseAddedAccessShare(waiter, resource, false)
		if e.locks.HasAccessShare(waiter, resource) {
			t.Fatal("canceled request left function grant")
		}
		if err := e.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		request, err = e.locks.StartAcquire(context.Background(), waiter, resource, lock.AccessShare, lock.Policy{})
		if request != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("old identity admitted after replacement", request, err)
		}
	})
	t.Run("descriptor_readmission_never_retargets", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		reader, _ := blockingUnitOwner(t, e)
		scope := pgrowlocksUnitScope(t, e, reader)
		ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: "typed"}}}
		stored := e.storage.Relations[d.Address()]
		e.mu.Lock()
		auth, err := scope.writeAuthorizationLocked(e)
		if err == nil {
			err = scope.pgrowlocksReadmitLocked(e, auth, name, ref, d, stored)
		}
		e.mu.Unlock()
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
		e.mu.Lock()
		auth, err = scope.writeAuthorizationLocked(e)
		if err == nil {
			err = scope.pgrowlocksReadmitLocked(e, auth, name, ref, d, stored)
		}
		e.mu.Unlock()
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("descriptor changed during admission", err)
		}
	})
}
