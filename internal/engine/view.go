package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func executeCreateView(e *core.Engine, scope *core.StatementScope, createView *parser.CreateView, count int) (core.Result, error) {
	op := scope.Op
	e.Mu.Lock()
	if err := scope.ValidateLockedRelation(e); err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	ns := scope.ViewCreationOrZero().Namespace
	if scope.ViewCreationOrZero().Changes != nil {
		auth.Catalog = e.PrivateCatalogLocked(scope.ViewCreationOrZero().Changes)
		auth.Recapture(auth.Catalog)
		if table, ok := scope.ViewCreationOrZero().Changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
			auth.StagedTemporary = table.Address()
		}
	}
	if err == nil {
		err = auth.CheckCreate(ns)
		if core.OperationReplayOrZero(op).TableDestination {
			bootstrap, guardErr := auth.Roles.Descriptor(auth.State.Authenticated)
			if guardErr != nil || !bootstrap.Superuser || e.ImportTargets == nil {
				e.Mu.Unlock()
				return core.Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
			}
			if denied, ok := err.(*sqlstate.Error); ok && denied.Code == "42501" {
				err = nil
			}
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	old, err := view.CreateTarget(auth.View, auth, createView, ns, scope.LockedRef)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	b := scope.ViewCreationOrZero().Binding
	if err := scope.ValidateQuerySources(b.QuerySources); err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	if count != 0 {
		e.Mu.Unlock()
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options, err := view.CheckCreate(auth.Catalog, createView, b, old)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	stage.AuthorizeLocked(auth, op.Session)
	defer e.DiscardCatalogStage(stage)
	err = view.StageCreate(ViewHost{TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, createView, old, ns, b, options, scope.ViewCreationOrZero().Persistence == "temporary")
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err := acquireViewDefinitionReferences(scope, b); err != nil {
		return core.Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	if scope.ViewCreationOrZero().Changes != nil {
		// TEMP definitions publish through the overlay rather than the ordinary
		// publication hook. Admit the same checked procedure sources before
		// either explicit or inferred TEMP state can enter that overlay.
		if err := scope.AcquireRoutineDefinitionProviders(stage); err != nil {
			return core.Result{}, err
		}
		scope.Owner.AccessedTemporary = true
		if err := e.StageCatalogOverlay(scope, stage, scope.ViewCreationOrZero().Changes); err != nil {
			return core.Result{}, err
		}
		return core.Result{}, nil
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.Completed = true
	}
	return core.Result{}, err
}
