package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func alterTableOfLocked(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.AlterTableOf, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	old, ok := scope.LockedDescriptor.(*catalog.Relation)
	if !ok {
		return fail(ids.ErrCatalogReplaced)
	}
	next, err := table.AlterTableOf(DDLHost{E: e, Op: op}, auth, old, s)
	if err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	if next.OfType != (ids.ObjectAddress{}) {
		stage.DomainBases = append(stage.DomainBases, auth.Catalog.Get(next.OfType))
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if s.Type != nil {
		// Execution can select a source created while target admission waited.
		// Grant the actual staged attachment, not just the pre-wait source.
		replacementRead := scope.ReplacementRead
		if err := acquireTypedSources(scope, stage, false); err != nil {
			return Result{}, err
		}
		// Release only this helper's incidental reservation before provider
		// callbacks; preserve any replacement admission held on entry.
		if !replacementRead && scope.ReplacementRead {
			e.ReplacementMu.RUnlock()
			scope.ReplacementRead = false
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.Completed = true
	}
	return Result{}, err
}
