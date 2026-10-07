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
	source           ObjectRef
	attributes       []int32
	labels           map[string]int32
	reads            []int32
	dependencies     []expressionDependency
	sequenceRefs     map[int]ObjectRef
	literals         map[viewLiteralPath]literalExpression
	barrier, invoker bool
	rule             OID
}

func bindViewOutput(query *BoundQuery, view *parser.CreateView) error {
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
	if _, exists := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name}]; exists {
		e.mu.Unlock()
		return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
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
	oid, err := e.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	rule, err := e.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	revision, err := e.revisionLocked()
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	definition := &viewDefinition{query: b.query, syntax: view.Query, source: b.ref, dependencies: b.dependencies, barrier: barrier, invoker: invoker, rule: rule}
	definition.literals = b.viewLiterals
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
	if b.relation != nil {
		definition.attributes = append([]int32(nil), b.relation.attributes...)
		definition.labels = make(map[string]int32, len(b.relation.columns))
		for i, column := range b.relation.columns {
			definition.labels[column.Name] = b.relation.attributes[i]
		}
		for _, slot := range b.usedColumns {
			definition.reads = append(definition.reads, b.relation.attributes[slot])
		}
		if namespace := e.state.descriptors[b.relation.namespace].(*namespaceDescriptor); namespace.temporary != (SessionToken{}) {
			e.mu.Unlock()
			return Result{}, sqlError("0A000", "temporary views are unsupported")
		}
	}
	d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: ns.Address, name: name, revision: revision, owner: auth.state.CurrentUser, relationKind: viewRelation, view: definition, columns: append([]ColumnDescriptor(nil), b.query.Root.Schema()...), index: make(map[string]int), attributes: make([]int32, len(b.query.Root.Schema()))}
	for i := range d.columns {
		d.index[d.columns[i].Name] = i
		d.attributes[i] = int32(i + 1)
		d.columns[i].OriginTable = uint32(oid)
		d.columns[i].OriginAttribute = int16(i + 1)
	}
	base := auth.catalog
	stage := e.BeginCatalogStage(base)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	err = stage.Create(d)
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
