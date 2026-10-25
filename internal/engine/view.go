package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Analyze once after admitting all body sources, before selecting the CREATE
// destination. Keep inferred persistence and private namespace off the held AST.
func (s *StatementScope) prepareViewCreation(createView *parser.CreateView) error {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, createView)
	if err != nil {
		return err
	}
	b, err := describe(s.op.Context(), e.analysisHost(s.op), createView, auth, ids.ObjectRef{}, s.input, nil, &s.queryLockAdmission)
	if err != nil {
		return err
	}
	if err := e.attachCTEViewTargetsLocked(s, &b, false); err != nil {
		return err
	}
	if err := s.validateQuerySources(b.QuerySources); err != nil {
		return err
	}
	if parser.HasModifyingCTE(createView.Query) {
		return sqlError("0A000", "views must not contain data-modifying statements in WITH")
	}
	persistence := view.RequestedViewPersistence(auth.Catalog, createView, b.QuerySources)
	if persistence == "temporary" && createView.Persistence != "temporary" {
		s.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`view "%s" will be a temporary view`, createView.Name.Parts[len(createView.Name.Parts)-1].Text)})
	}
	if err := view.CheckColumnCollations(b.Query.Root); err != nil {
		return err
	}
	changes := s.owner.Catalog.Clone()
	ns, err := e.relationCreationNamespaceLocked(auth, createView.Name, persistence, changes)
	if err != nil {
		return err
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == s.op.Session.SessionToken {
		auth.stagedTemporary = table.Address()
	}
	if err := auth.CheckCreate(ns); err != nil && !operationReplayOrZero(s.op).tableDestination {
		return err
	}
	persistence, err = access.AdjustRelationCreationPersistence(auth.View, auth, ns, persistence)
	if err != nil {
		return err
	}
	s.viewCreation().Namespace, s.viewCreation().Persistence, s.viewCreation().Binding = ns, persistence, b
	if persistence == "temporary" {
		s.viewCreation().Changes = changes
	}
	return nil
}

func (e *Engine) executeCreateView(scope *StatementScope, createView *parser.CreateView, count int) (Result, error) {
	op := scope.op
	e.mu.Lock()
	if err := scope.validateLockedRelation(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	ns := scope.viewCreationOrZero().Namespace
	if scope.viewCreationOrZero().Changes != nil {
		auth.Catalog = e.privateCatalogLocked(scope.viewCreationOrZero().Changes)
		auth.Recapture(auth.Catalog)
		if table, ok := scope.viewCreationOrZero().Changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
			auth.stagedTemporary = table.Address()
		}
	}
	if err == nil {
		err = auth.CheckCreate(ns)
		if operationReplayOrZero(op).tableDestination {
			bootstrap, guardErr := auth.Roles.Descriptor(auth.State.Authenticated)
			if guardErr != nil || !bootstrap.Superuser || e.importTargets == nil {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
			}
			if denied, ok := err.(*sqlstate.Error); ok && denied.Code == "42501" {
				err = nil
			}
		}
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	old, err := view.CreateTarget(auth.View, auth, createView, ns, scope.lockedRef)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	b := scope.viewCreationOrZero().Binding
	if err := scope.validateQuerySources(b.QuerySources); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options, err := view.CheckCreate(auth.Catalog, createView, b, old)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	stage.authorizeLocked(auth, op.Session)
	defer e.DiscardCatalogStage(stage)
	err = view.StageCreate(ddlHost{e: e, op: op, stage: stage}, auth, createView, old, ns, b, options, scope.viewCreationOrZero().Persistence == "temporary")
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err := scope.acquireViewDefinitionReferences(b); err != nil {
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	if scope.viewCreationOrZero().Changes != nil {
		// TEMP definitions publish through the overlay rather than the ordinary
		// publication hook. Admit the same checked procedure sources before
		// either explicit or inferred TEMP state can enter that overlay.
		if err := scope.acquireRoutineDefinitionProviders(stage); err != nil {
			return Result{}, err
		}
		scope.owner.AccessedTemporary = true
		if err := e.stageCatalogOverlay(scope, stage, scope.viewCreationOrZero().Changes); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

// viewCreation returns the statement's viewCreation slot, allocating it on first use.
func (s *StatementScope) viewCreation() *view.CreationScope {
	if s.viewCreationSlot == nil {
		s.viewCreationSlot = new(view.CreationScope)
	}
	return s.viewCreationSlot
}

// viewCreationOrZero reads the viewCreation slot; a nil slot reads as zero.
func (s *StatementScope) viewCreationOrZero() view.CreationScope {
	if s.viewCreationSlot == nil {
		return view.CreationScope{}
	}
	return *s.viewCreationSlot
}

// setActiveViews installs a saved view-expansion stack; restoring an empty
// stack never allocates the slot.
func (s *StatementScope) setActiveViews(active []ids.ObjectAddress) {
	if active != nil || s.viewCreationSlot != nil {
		s.viewCreation().Active = active
	}
}
