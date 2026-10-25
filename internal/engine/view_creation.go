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
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(s.Op, createView)
	if err != nil {
		return err
	}
	b, err := Describe(s.Op.Context(), e.AnalysisHost(s.Op), createView, auth, ids.ObjectRef{}, s.Input, nil, &s.QueryLockAdmission)
	if err != nil {
		return err
	}
	if err := e.attachCTEViewTargetsLocked(s, &b, false); err != nil {
		return err
	}
	if err := s.ValidateQuerySources(b.QuerySources); err != nil {
		return err
	}
	if parser.HasModifyingCTE(createView.Query) {
		return SQLError("0A000", "views must not contain data-modifying statements in WITH")
	}
	persistence := view.RequestedViewPersistence(auth.Catalog, createView, b.QuerySources)
	if persistence == "temporary" && createView.Persistence != "temporary" {
		s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`view "%s" will be a temporary view`, createView.Name.Parts[len(createView.Name.Parts)-1].Text)})
	}
	if err := view.CheckColumnCollations(b.Query.Root); err != nil {
		return err
	}
	changes := s.Owner.Catalog.Clone()
	ns, err := e.RelationCreationNamespaceLocked(auth, createView.Name, persistence, changes)
	if err != nil {
		return err
	}
	auth.Catalog = e.PrivateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == s.Op.Session.SessionToken {
		auth.StagedTemporary = table.Address()
	}
	if err := auth.CheckCreate(ns); err != nil && !OperationReplayOrZero(s.Op).TableDestination {
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

// viewCreation returns the statement's viewCreation slot, allocating it on first use.
func (s *StatementScope) viewCreation() *view.CreationScope {
	if s.viewCreationSlot == nil {
		s.viewCreationSlot = new(view.CreationScope)
	}
	return s.viewCreationSlot
}

// ViewCreationOrZero reads the viewCreation slot; a nil slot reads as zero.
func (s *StatementScope) ViewCreationOrZero() view.CreationScope {
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
