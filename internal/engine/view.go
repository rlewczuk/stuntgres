package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A definition owns an immutable analyzed query and its creation-time source layout.
// Runtime source opening uses identities, never the retained deparser syntax.
type viewDefinition struct {
	query            *BoundQuery
	syntax           *parser.Select
	sources          []viewLeaf
	dependencies     []expressionDependency
	sequenceRefs     map[int]ObjectRef
	literals         map[viewLiteralPath]literalExpression
	columns          map[viewLiteralPath]int
	stars            map[int][]int
	namespaces       []viewNamespace
	barrier, invoker bool
	rule             OID
}

// Persistent leaves contain identities and frozen layout, never live storage or
// descriptors. Ordinals are SQL occurrences, including repeated self-joins.
type viewLeaf struct {
	ref        ObjectRef
	attributes []int32
	reads      []int32
}

// Namespace labels and frame slots are immutable analysis metadata, not an
// execution node. Leaf/attribute identities survive current positional changes;
// computed USING slots have no leaf identity.
type viewNamespaceColumn struct {
	name       string
	slot, leaf int
	attribute  int32
}
type viewNamespaceQualifier struct {
	name    string
	columns []viewNamespaceColumn
}
type viewNamespace struct {
	columns    []viewNamespaceColumn
	qualifiers []viewNamespaceQualifier
	keys       []string
}

func (b *expressionBinder) freezeViewColumns(columns []queryColumn) []viewNamespaceColumn {
	out := make([]viewNamespaceColumn, len(columns))
	for i, c := range columns {
		out[i] = viewNamespaceColumn{name: c.descriptor.Name, slot: c.slot, leaf: c.leaf}
		if c.leaf >= 0 {
			out[i].attribute = b.querySources[c.leaf].relation.attributes[c.physical]
		}
	}
	return out
}

func (e *Engine) bindViewOutput(query *BoundQuery, view *parser.CreateView, auth AuthorizationView) error {
	columns := query.Root.Schema()
	if len(view.Columns) > len(columns) {
		return sqlError("42601", "CREATE VIEW specifies more column names than columns")
	}
	seen := make(map[string]bool, len(columns))
	for i := range columns {
		if i < len(view.Columns) {
			columns[i].Name = view.Columns[i].Text
		}
		if seen[columns[i].Name] {
			if view.Replace {
				// OR REPLACE of an absent target uses ordinary CREATE diagnostics.
				ns, err := e.creationNamespace(auth, view.Name)
				if err == nil {
					name := view.Name.Parts[len(view.Name.Parts)-1].Text
					if _, exists := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name}]; exists {
						return sqlError("42701", fmt.Sprintf(`column "%s" of relation "%s" already exists`, columns[i].Name, name))
					}
				}
			}
			return sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, columns[i].Name))
		}
		seen[columns[i].Name] = true
		columns[i].Nullable, columns[i].NullabilityKnown = true, true
	}
	return nil
}

func viewSecurity(options []parser.ViewOption) (barrier, invoker bool, err error) {
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		name := option.Name.Text
		if name == "check_option" {
			return false, false, sqlError("0A000", "view check options are unsupported")
		}
		if name != "security_barrier" && name != "security_invoker" {
			return false, false, sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
		}
		if seen[name] {
			return false, false, sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, name))
		}
		seen[name] = true
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
			return false, false, sqlError("22023", fmt.Sprintf(`invalid value for boolean option "%s": %s`, name, option.Value))
		}
		if name == "security_barrier" {
			barrier = value
		} else {
			invoker = value
		}
	}
	return
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
	ns, err := e.creationNamespace(auth, view.Name)
	if err == nil && view.Replace && ns != scope.viewCreationNamespace {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	if err == nil {
		err = checkTableCreationNamespace(auth.catalog, ns.Address)
	}
	if err == nil {
		err = auth.CheckCreate(ns)
		if op.restoreTableDestination {
			bootstrap, guardErr := auth.roles.Descriptor(auth.state.Authenticated)
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
		return Result{}, creationError(err, view.Name)
	}
	name := view.Name.Parts[len(view.Name.Parts)-1].Text
	var old *relationDescriptor
	if address, exists := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name}]; exists {
		if !view.Replace {
			e.mu.Unlock()
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		old = e.state.descriptors[address].(*relationDescriptor)
		ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: address}
		if ref != scope.lockedRef {
			e.mu.Unlock()
			return Result{}, ErrCatalogReplaced
		}
		if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if old.relationKind != viewRelation {
			e.mu.Unlock()
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is not a view`, name))
		}
	} else if scope.lockedRef.owner != nil {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	lookupAuth, err := e.statementAuthorizationLocked(op, view)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	b, err := e.describe(op.Context(), view, lookupAuth, scope.lockedRef, scope.input, op, nil)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	barrier, invoker, err := viewSecurity(view.Options)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	projection, ok := b.query.Root.(*projectNode)
	if !ok {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: view lacks final projection")
	}
	for i, expression := range projection.expressions {
		if expression.CollationIndeterminate() {
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: fmt.Sprintf(`could not determine which collation to use for view column "%s"`, projection.Schema()[i].Name), Hint: "Use the COLLATE clause to set the collation explicitly."}
		}
	}
	var oid, rule OID
	if old != nil {
		if err := checkViewPrefix(old.columns, b.query.Root.Schema()); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		oid, rule = old.address.ObjectID, old.view.rule
	} else {
		oid, err = e.allocateOIDLocked()
		if err == nil {
			rule, err = e.allocateOIDLocked()
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	revision, err := e.revisionLocked()
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	definition := &viewDefinition{query: b.query, syntax: view.Query, dependencies: b.dependencies, barrier: barrier, invoker: invoker, rule: rule}
	definition.literals = b.viewLiterals
	definition.columns, definition.stars = b.viewColumns, b.viewStars
	definition.namespaces = b.viewNamespaces
	definition.sequenceRefs = make(map[int]ObjectRef, len(b.sequenceConstants))
	for _, constant := range b.sequenceConstants {
		if constant.temporary != (SessionToken{}) {
			e.mu.Unlock()
			return Result{}, sqlError("0A000", "temporary views are unsupported")
		}
		definition.sequenceRefs[constant.offset] = constant.ref
		if _, exists := e.state.descriptors[constant.ref.Address]; exists {
			definition.dependencies = append(definition.dependencies, expressionDependency{ref: constant.ref, revision: constant.stamp})
		}
	}
	definition.sources = make([]viewLeaf, len(b.querySources))
	for occurrence, source := range b.querySources {
		leaf := &definition.sources[occurrence]
		leaf.ref = source.ref
		leaf.attributes = append([]int32(nil), source.relation.attributes...)
		for _, slot := range source.uses {
			leaf.reads = append(leaf.reads, source.relation.attributes[slot])
		}
		if namespace := e.state.descriptors[source.relation.namespace].(*namespaceDescriptor); namespace.temporary != (SessionToken{}) {
			e.mu.Unlock()
			return Result{}, sqlError("0A000", "temporary views are unsupported")
		}
	}
	d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: ns.Address, name: name, revision: revision, owner: auth.state.CurrentUser, relationKind: viewRelation, view: definition, columns: append([]ColumnDescriptor(nil), b.query.Root.Schema()...), index: make(map[string]int), attributes: make([]int32, len(b.query.Root.Schema()))}
	if old != nil {
		*d = *old
		d.revision, d.view = revision, definition
		d.columns = append([]ColumnDescriptor(nil), b.query.Root.Schema()...)
		d.attributes = make([]int32, len(d.columns))
		copy(d.attributes, old.attributes)
		d.index = make(map[string]int, len(d.columns))
	}
	attribute := d.attributeHighWater
	for _, id := range d.attributes {
		if id > attribute {
			attribute = id
		}
	}
	for i := range d.columns {
		d.index[d.columns[i].Name] = i
		if old == nil || i >= len(old.attributes) {
			attribute++
			d.attributes[i] = attribute
		}
		d.columns[i].OriginTable = uint32(oid)
		d.columns[i].OriginAttribute = int16(d.attributes[i])
	}
	d.attributeHighWater = attribute
	base := auth.catalog
	stage := e.BeginCatalogStage(base)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	if old == nil {
		err = stage.Create(d)
	} else {
		err = stage.Replace(d)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
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
	_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func checkViewPrefix(old, next []ColumnDescriptor) error {
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
			oldType, err := pgType(a)
			if err != nil {
				return err
			}
			newType, err := pgType(b)
			if err != nil {
				return err
			}
			return sqlError("42P16", fmt.Sprintf(`cannot change data type of view column "%s" from %s to %s`, column.Name, oldType, newType))
		}
		if a.Collation.ID != b.Collation.ID {
			return sqlError("42P16", fmt.Sprintf(`cannot change collation of view column "%s" from "%s" to "%s"`, column.Name, builtinCollationName(a.Collation.ID), builtinCollationName(b.Collation.ID)))
		}
	}
	return nil
}

func (v *viewDefinition) validateNamespaces() error {
	ordinal, occurrence, join := 0, 0, 0
	values := 0
	var visit func(parser.FromItem) (LogicalNode, error)
	visit = func(item parser.FromItem) (LogicalNode, error) {
		if item == nil {
			return nil, nil
		}
		if ordinal >= len(v.namespaces) {
			return nil, fmt.Errorf("stuntgres: missing view namespace")
		}
		ns := v.namespaces[ordinal]
		ordinal++
		var root LogicalNode
		switch n := item.(type) {
		case *parser.ValuesQuery:
			root = viewValuesAt(v.query.Root, values)
			values++
		case *parser.TableReference:
			root = viewSourceAt(viewFromRoot(v.query.Root), occurrence)
			occurrence++
		case *parser.AliasReference:
			child, err := visit(n.Input)
			if err != nil {
				return nil, err
			}
			root = child
		case *parser.JoinReference:
			root = viewJoinAt(viewFromRoot(v.query.Root), join)
			join++
			if bound, ok := root.(*joinNode); !ok || bound.kind != n.Kind || n.Kind > parser.JoinFull {
				return nil, fmt.Errorf("stuntgres: inconsistent view join kind")
			}
			if _, err := visit(n.Left); err != nil {
				return nil, err
			}
			if _, err := visit(n.Right); err != nil {
				return nil, err
			}
			if len(ns.keys) > len(ns.columns) {
				return nil, fmt.Errorf("stuntgres: invalid view USING namespace")
			}
			for i, key := range ns.keys {
				if ns.columns[i].name != key {
					return nil, fmt.Errorf("stuntgres: invalid view USING key label")
				}
			}
		default:
			return nil, fmt.Errorf("stuntgres: invalid view namespace syntax")
		}
		if root == nil {
			return nil, fmt.Errorf("stuntgres: missing view namespace frame")
		}
		check := func(columns []viewNamespaceColumn) error {
			for _, c := range columns {
				if c.slot < 0 || c.slot >= len(root.Schema()) {
					return fmt.Errorf("stuntgres: invalid view namespace slot")
				}
				if c.leaf >= 0 {
					if c.leaf >= len(v.sources) {
						return fmt.Errorf("stuntgres: invalid view namespace occurrence")
					}
					found := false
					for _, attribute := range v.sources[c.leaf].attributes {
						found = found || attribute == c.attribute
					}
					if !found {
						return fmt.Errorf("stuntgres: invalid view namespace attribute")
					}
				} else if c.attribute != 0 {
					return fmt.Errorf("stuntgres: invalid computed view namespace identity")
				}
			}
			return nil
		}
		if err := check(ns.columns); err != nil {
			return nil, err
		}
		for _, q := range ns.qualifiers {
			if err := check(q.columns); err != nil {
				return nil, err
			}
		}
		return root, nil
	}
	if _, err := visit(v.syntax.From); err != nil {
		return err
	}
	if ordinal != len(v.namespaces) || occurrence != len(v.sources) {
		return fmt.Errorf("stuntgres: extra view namespace")
	}
	return nil
}
