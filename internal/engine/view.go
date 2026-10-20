package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A definition owns an immutable analyzed query and its creation-time source layout.
// Runtime source opening uses identities, never the retained deparser syntax.
type viewDefinition struct {
	query             *plan.Query
	syntax            *parser.Select
	expressionSyntax  parser.Expr // Lexical outer frame for a frozen Boolean definition.
	sources           []viewLeaf
	dependencies      []catalog.ExpressionDependency
	sequenceRefs      map[int]ids.ObjectRef
	literals          map[analyze.ViewLiteralPath]plan.LiteralExpr
	castUsages        map[analyze.ViewLiteralPath]sqltype.Usage
	routineRefs       map[analyze.ViewLiteralPath]ids.ObjectRef
	operatorRefs      map[analyze.ViewLiteralPath]ids.ObjectRef
	composites        map[analyze.ViewLiteralPath]analyze.CompositeOccurrence
	similarSubstrings map[analyze.ViewLiteralPath]bool
	posixSubstrings   map[analyze.ViewLiteralPath]bool
	columns           map[analyze.ViewLiteralPath]analyze.ViewColumn
	stars             map[analyze.ViewLiteralPath][]analyze.ViewColumn
	frames            map[uint32]*plan.ProjectNode
	queries           map[*parser.Select]*plan.ProjectNode
	namespaces        []analyze.ViewNamespace
	barrier, invoker  bool
	checkOption       viewCheckMode
	options           []string // Immutable ordered installed name=value facts, including explicit false.
	rule              ids.OID
	consumer          ids.ObjectAddress
	temporary         ids.SessionToken
	references        map[ids.ObjectRef]catalog.Descriptor
	collations        map[analyze.ViewLiteralPath]uint32
	quoteRevision     ids.DefinitionRevision // frozen defining revision, not later relation renames
}

// Stored leaves contain identities and frozen layout, never live storage or
// descriptors. Ordinals are SQL occurrences, including repeated self-joins.
type viewLeaf struct {
	ref          ids.ObjectRef
	only         bool
	attributes   []int32
	reads        []int32
	analysisOnly bool
	mark         plan.RowMark
}

// Output names are downstream definition checks, not SELECT/alias analysis.
// Existing replacements check their descriptor before options; fresh CREATE
// checks options first. The caller has already resolved the real destination.
func checkViewOutputNames(columns []catalog.Column, old *catalog.Relation) error {
	if len(columns) < 2 {
		return nil
	}
	seen := make(map[string]bool, len(columns))
	for _, column := range columns {
		if seen[column.Name] {
			if old != nil {
				return sqlError("42701", fmt.Sprintf(`column "%s" of relation "%s" already exists`, column.Name, old.Name()))
			}
			return sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		seen[column.Name] = true
	}
	return nil
}

func viewOptions(options []parser.ViewOption) (barrier, invoker bool, mode viewCheckMode, err error) {
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		name := option.Name.Text
		if name != "check_option" && name != "security_barrier" && name != "security_invoker" {
			return false, false, viewCheckNone, sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
		}
		if seen[name] {
			return false, false, viewCheckNone, sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, name))
		}
		seen[name] = true
		if name == "check_option" {
			switch {
			case strings.EqualFold(option.Value, "local"):
				mode = viewCheckLocal
			case strings.EqualFold(option.Value, "cascaded"):
				mode = viewCheckCascaded
			default:
				return false, false, viewCheckNone, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for enum option "check_option": %s`, option.Value), Detail: `Valid values are "local" and "cascaded".`}
			}
			continue
		}
		value, valid := false, false
		text := strings.ToLower(strings.TrimSpace(option.Value))
		if text == "1" {
			value, valid = true, true
		} else if text == "0" {
			valid = true
		} else if text != "" {
			for _, spelling := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(spelling, text) {
					value, valid = true, true
				}
			}
			for _, spelling := range []string{"false", "no", "off"} {
				if strings.HasPrefix(spelling, text) {
					if valid {
						valid = false
						break
					}
					valid = true
				}
			}
		}
		if !valid {
			return false, false, viewCheckNone, sqlError("22023", fmt.Sprintf(`invalid value for boolean option "%s": %s`, name, option.Value))
		}
		if name == "security_barrier" {
			barrier = value
		} else {
			invoker = value
		}
	}
	return
}

// Relation occurrences, including unused/join/subquery leaves, determine PG18
// inference. Expression-only regclass/sequence dependencies deliberately do not.
func requestedViewPersistence(catalog catalog.Read, view *parser.CreateView, sources []exec.Leaf) string {
	if view.Persistence == "temporary" {
		return "temporary"
	}
	for _, source := range sources {
		if namespace, ok := catalog.Namespace(source.Relation.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
			return "temporary"
		}
	}
	return "logged"
}

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
		s.op.EmitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`view "%s" will be a temporary view`, view.Name.Parts[len(view.Name.Parts)-1].Text)})
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
	changes := s.owner.catalog.Clone()
	ns, err := e.relationCreationNamespaceLocked(auth, view.Name, persistence, changes)
	if err != nil {
		return err
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == s.op.Session.token {
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
		if table, ok := scope.viewCreationOrZero().changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.token {
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
			if err := validateViewCheckDeclaration(&viewDefinition{syntax: view.Query, query: b.Query}); err != nil {
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
	barrier, invoker, mode, err := viewOptions(view.Options)
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
		oid, rule = old.Address().ObjectID, viewOf(old).rule
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
	definition.checkOption = mode
	definition.options = transformViewOptions(nil, view.Options, false)
	definition.quoteRevision = revision
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns.Address, ObjectName: name, ObjectRevision: revision, Owner: auth.State.CurrentUser, RelationKind: catalog.ViewRelation, View: viewPayloadOf(definition), Columns: append([]catalog.Column(nil), b.Query.Root.Schema()...), Index: make(map[string]int), Attributes: make([]int32, len(b.Query.Root.Schema()))}
	if old != nil {
		*d = *old
		d.ObjectRevision, d.View = revision, viewPayloadOf(definition)
		d.Columns = append([]catalog.Column(nil), b.Query.Root.Schema()...)
		d.Attributes = make([]int32, len(d.Columns))
		copy(d.Attributes, old.Attributes)
		d.Index = make(map[string]int, len(d.Columns))
	}
	if old == nil {
		d.Security.ACL = stage.creationACL(d.Owner, d.Namespace(), authorization.DefaultTables)
	}
	if scope.viewCreationOrZero().persistence == "temporary" {
		definition.consumer = d.Address()
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
		scope.owner.accessedTemporary = true
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

func checkViewPrefix(catalog catalog.Read, old, next []catalog.Column) error {
	if len(next) < len(old) {
		return sqlError("42P16", "cannot drop columns from view")
	}
	for i, column := range old {
		replacement := next[i]
		if column.Name != replacement.Name {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P16", Message: fmt.Sprintf(`cannot change name of view column "%s" to "%s"`, column.Name, replacement.Name), Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}
		}
		a, b := column.Usage, replacement.Usage
		if a.Type.ID() != b.Type.ID() || a.Typmod != b.Typmod {
			oldType, err := analyze.PGType(a)
			if err != nil {
				return err
			}
			newType, err := analyze.PGType(b)
			if err != nil {
				return err
			}
			return sqlError("42P16", fmt.Sprintf(`cannot change data type of view column "%s" from %s to %s`, column.Name, oldType, newType))
		}
		if a.Collation.ID != b.Collation.ID {
			return sqlError("42P16", fmt.Sprintf(`cannot change collation of view column "%s" from "%s" to "%s"`, column.Name, typedef.CollationName(catalog, a.Collation.ID), typedef.CollationName(catalog, b.Collation.ID)))
		}
	}
	return nil
}

func (v *viewDefinition) validateNamespaces() error {
	expected := 0
	var countFrom func(parser.FromItem)
	countFrom = func(item parser.FromItem) {
		if item == nil {
			return
		}
		expected++
		switch n := item.(type) {
		case *parser.AliasReference:
			countFrom(n.Input)
		case *parser.JoinReference:
			countFrom(n.Left)
			countFrom(n.Right)
		}
	}
	for _, project := range v.frames {
		if project.Syntax != nil {
			countFrom(project.Syntax.From)
		}
	}
	if expected != len(v.namespaces) {
		return fmt.Errorf("stuntgres: invalid view namespace count")
	}
	for _, ns := range v.namespaces {
		project := v.frames[ns.Frame]
		if project == nil {
			return fmt.Errorf("stuntgres: missing view namespace frame")
		}
		if ns.Node < analyze.ViewSourceNamespace || ns.Node > analyze.ViewRegexpNamespace {
			return fmt.Errorf("stuntgres: invalid view namespace kind")
		}
		root := v.namespaceRoot(ns)
		if root == nil {
			return fmt.Errorf("stuntgres: missing view namespace node")
		}
		check := func(columns []analyze.ViewNamespaceColumn) error {
			for _, column := range columns {
				if column.Slot < 0 || column.Slot >= len(root.Schema()) {
					return fmt.Errorf("stuntgres: invalid view namespace slot")
				}
				if column.Leaf >= 0 {
					if column.Leaf >= len(v.sources) {
						return fmt.Errorf("stuntgres: invalid view namespace occurrence")
					}
					found := false
					for _, attribute := range v.sources[column.Leaf].attributes {
						found = found || attribute == column.Attribute
					}
					if !found {
						return fmt.Errorf("stuntgres: invalid view namespace attribute")
					}
				} else if column.Attribute != 0 {
					return fmt.Errorf("stuntgres: invalid computed view namespace identity")
				}
			}
			return nil
		}
		if err := check(ns.Columns); err != nil {
			return err
		}
		for _, q := range ns.Qualifiers {
			if err := check(q.Columns); err != nil {
				return err
			}
		}
		if len(ns.Keys) > len(ns.Columns) {
			return fmt.Errorf("stuntgres: invalid view USING namespace")
		}
		for i, key := range ns.Keys {
			if ns.Columns[i].Name != key {
				return fmt.Errorf("stuntgres: invalid view USING key label")
			}
		}
	}
	return nil
}

func (v *viewDefinition) namespaceRoot(ns analyze.ViewNamespace) plan.Node {
	project := v.frames[ns.Frame]
	if project == nil {
		return nil
	}
	from := analyze.ViewFromRoot(project)
	switch ns.Node {
	case analyze.ViewSourceNamespace:
		return viewSourceAt(from, ns.Ordinal)
	case analyze.ViewJoinNamespace:
		return viewJoinAt(from, ns.Ordinal)
	case analyze.ViewValuesNamespace:
		return viewValuesAt(from, ns.Ordinal)
	case analyze.ViewFunctionNamespace:
		return viewFunctionFromAt(from, ns.Ordinal, false)
	case analyze.ViewRegexpNamespace:
		return viewFunctionFromAt(from, ns.Ordinal, true)
	case analyze.ViewDerivedNamespace:
		if ns.Ordinal < 0 {
			return viewCTEOrdinal(from, ns.Ordinal)
		}
		if child := v.frames[uint32(ns.Ordinal)]; child != nil {
			return viewDerivedAt(from, child.Syntax)
		}
	}
	return nil
}

// viewCreationScope is the statement's view slot: the CREATE VIEW target
// namespace, persistence, binding and staged changes, plus the stack of views
// being expanded (for recursion checks).
type viewCreationScope struct {
	namespace   ids.ObjectRef
	persistence string
	binding     analyze.Binding
	changes     *catalogChanges
	active      []ids.ObjectAddress
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

// endStatement clears the view-creation state when the statement finishes.
// The active view stack is lexical expansion state and is left as it was.
func (v *viewCreationScope) endStatement() {
	if v == nil {
		return
	}
	v.namespace, v.persistence, v.binding, v.changes = ids.ObjectRef{}, "", analyze.Binding{}, nil
}

// setActiveViews installs a saved view-expansion stack; restoring an empty
// stack never allocates the slot.
func (s *StatementScope) setActiveViews(active []ids.ObjectAddress) {
	if active != nil || s.viewCreationSlot != nil {
		s.viewCreation().active = active
	}
}
