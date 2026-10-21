package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Analyze once after admitting all body sources, before selecting the CREATE
// destination. Keep inferred persistence and private namespace off the held AST.
func (s *StatementScope) prepareViewCreation(view *parser.CreateView) error {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, view)
	if err != nil {
		return err
	}
	b, err := describe(s.op.Context(), e.analysisHost(s.op), view, auth, ids.ObjectRef{}, s.input, nil, &s.queryLockAdmission)
	if err != nil {
		return err
	}
	if err := e.attachCTEViewTargetsLocked(s, &b, false); err != nil {
		return err
	}
	if err := s.validateQuerySources(b.QuerySources); err != nil {
		return err
	}
	if parser.HasModifyingCTE(view.Query) {
		return sqlError("0A000", "views must not contain data-modifying statements in WITH")
	}
	persistence := requestedViewPersistence(auth.Catalog, view, b.QuerySources)
	if persistence == "temporary" && view.Persistence != "temporary" {
		s.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`view "%s" will be a temporary view`, view.Name.Parts[len(view.Name.Parts)-1].Text)})
	}
	projection, ok := b.Query.Root.(*plan.ProjectNode)
	if !ok {
		return fmt.Errorf("stuntgres: view lacks final projection")
	}
	for i, expression := range projection.Expressions {
		if expression.CollationIndeterminate() {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: fmt.Sprintf(`could not determine which collation to use for view column "%s"`, projection.Schema()[i].Name), Hint: "Use the COLLATE clause to set the collation explicitly."}
		}
	}
	changes := s.owner.Catalog.Clone()
	ns, err := e.relationCreationNamespaceLocked(auth, view.Name, persistence, changes)
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
	persistence, err = adjustRelationCreationPersistence(auth, ns, persistence)
	if err != nil {
		return err
	}
	s.viewCreation().namespace, s.viewCreation().persistence, s.viewCreation().binding = ns, persistence, b
	if persistence == "temporary" {
		s.viewCreation().changes = changes
	}
	return nil
}

func (e *Engine) executeCreateView(scope *StatementScope, view *parser.CreateView, count int) (Result, error) {
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
	ns := scope.viewCreationOrZero().namespace
	if scope.viewCreationOrZero().changes != nil {
		auth.Catalog = e.privateCatalogLocked(scope.viewCreationOrZero().changes)
		auth.Recapture(auth.Catalog)
		if table, ok := scope.viewCreationOrZero().changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
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
	name := view.Name.Parts[len(view.Name.Parts)-1].Text
	var old *catalog.Relation
	if address, exists := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}); exists {
		if !view.Replace {
			e.mu.Unlock()
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		old = auth.Catalog.MustRelation(address)
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), address)
		if ref != scope.lockedRef {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if old.RelationKind != catalog.ViewRelation {
			e.mu.Unlock()
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is not a view`, name))
		}
	} else if scope.lockedRef.Domain() != nil {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	b := scope.viewCreationOrZero().binding
	if err := scope.validateQuerySources(b.QuerySources); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	// CREATE checks local declaration shape before options and replacement prefix.
	for _, option := range view.Options {
		if option.Name.Text == "check_option" {
			if err := stored.ValidateViewCheckDeclaration(stored.NewView(stored.ViewParts{Syntax: view.Query, Query: b.Query})); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			break
		}
	}
	if old != nil {
		if err := checkViewOutputNames(b.Query.Root.Schema(), old); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err := checkViewPrefix(auth.Catalog, old.Columns, b.Query.Root.Schema()); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	for _, column := range b.Query.Root.Schema() {
		if sqltype.OccurrenceRecordArray(column.Usage.Type) || column.Usage.Type.ID() == 2249 && column.Usage.Type.IsComposite() {
			e.mu.Unlock()
			typeName := "record"
			if sqltype.OccurrenceRecordArray(column.Usage.Type) {
				typeName = "record[]"
			}
			return Result{}, sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type %s`, column.Name, typeName))
		}
	}
	// Existing replacement shape errors precede the creation-only VOID
	// boundary. Appended VOID columns and CREATE OR REPLACE of a missing
	// destination still fail before a catalog stage can publish anything.
	if view.Replace {
		if err := analyze.CheckViewOutputTypes(b.Query.Root.Schema()); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	barrier, invoker, mode, err := stored.ViewOptions(view.Options)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if old == nil {
		if err := checkViewOutputNames(b.Query.Root.Schema(), nil); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	stage.authorizeLocked(auth, op.Session)
	defer e.DiscardCatalogStage(stage)
	if old == nil {
		available, displacementErr := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
		if displacementErr != nil {
			e.mu.Unlock()
			return Result{}, displacementErr
		}
		if !available {
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, name), Hint: "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."}
		}
	}
	var oid, rule ids.OID
	if old != nil {
		oid, rule = old.Address().ObjectID, stored.ViewOf(old).RuleOID()
	} else {
		oid, err = e.catalog.allocateOIDLocked()
		if err == nil {
			rule, err = e.catalog.allocateOIDLocked()
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	definition, err := e.freezeViewDefinitionLocked(auth, view.Query, b, barrier, invoker, rule, catalog.ViewRelation, scope.viewCreationOrZero().persistence == "temporary")
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	definition = definition.With(func(p *stored.ViewParts) {
		p.CheckOption = mode
		p.Options = transformViewOptions(nil, view.Options, false)
		p.QuoteRevision = revision
	})
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns.Address, ObjectName: name, ObjectRevision: revision, Owner: auth.State.CurrentUser, RelationKind: catalog.ViewRelation, View: stored.ViewPayload(definition), Columns: append([]catalog.Column(nil), b.Query.Root.Schema()...), Index: make(map[string]int), Attributes: make([]int32, len(b.Query.Root.Schema()))}
	if old != nil {
		*d = *old
		d.ObjectRevision, d.View = revision, stored.ViewPayload(definition)
		d.Columns = append([]catalog.Column(nil), b.Query.Root.Schema()...)
		d.Attributes = make([]int32, len(d.Columns))
		copy(d.Attributes, old.Attributes)
		d.Index = make(map[string]int, len(d.Columns))
	}
	if old == nil {
		d.Security.ACL = stage.creationACL(d.Owner, d.Namespace(), authorization.DefaultTables)
	}
	if scope.viewCreationOrZero().persistence == "temporary" {
		definition = definition.With(func(p *stored.ViewParts) { p.Consumer = d.Address() })
		d.View = stored.ViewPayload(definition)
	}
	attribute := d.ColumnMeta.HighWater
	for _, id := range d.Attributes {
		if id > attribute {
			attribute = id
		}
	}
	for i := range d.Columns {
		d.Index[d.Columns[i].Name] = i
		if old == nil || i >= len(old.Attributes) {
			attribute++
			d.Attributes[i] = attribute
		}
		d.Columns[i].OriginTable = uint32(oid)
		d.Columns[i].OriginAttribute = int16(d.Attributes[i])
		d.Columns[i].DeclaredDimensions = 0
	}
	d.ColumnMeta.HighWater = attribute
	if err = e.stageRowTypeLocked(stage, d); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if old == nil {
		err = stage.Create(d)
	} else {
		err = stage.Replace(d)
	}
	if err == nil && old == nil && stage.OldNameCount() != 0 {
		err = e.reownStagedTypeGenerationsLocked(stage)
	}
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
	if scope.viewCreationOrZero().changes != nil {
		scope.owner.AccessedTemporary = true
		if err := e.stageCatalogOverlay(scope, stage, scope.viewCreationOrZero().changes); err != nil {
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
func (s *StatementScope) viewCreation() *viewCreationScope {
	if s.viewCreationSlot == nil {
		s.viewCreationSlot = new(viewCreationScope)
	}
	return s.viewCreationSlot
}

// viewCreationOrZero reads the viewCreation slot; a nil slot reads as zero.
func (s *StatementScope) viewCreationOrZero() viewCreationScope {
	if s.viewCreationSlot == nil {
		return viewCreationScope{}
	}
	return *s.viewCreationSlot
}

// setActiveViews installs a saved view-expansion stack; restoring an empty
// stack never allocates the slot.
func (s *StatementScope) setActiveViews(active []ids.ObjectAddress) {
	if active != nil || s.viewCreationSlot != nil {
		s.viewCreation().active = active
	}
}
