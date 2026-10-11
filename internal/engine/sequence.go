package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (v AuthorizationView) checkSequenceAny(ref ObjectRef, mask Privileges) error {
	d, err := v.object(ref)
	if err != nil {
		return err
	}
	rights, err := v.Effective(ref, false)
	if err != nil {
		return err
	}
	if rights&mask != 0 {
		return nil
	}
	return sqlError("42501", fmt.Sprintf("permission denied for sequence %s", d.Name()))
}
func sequenceKind(d *relationDescriptor) error {
	if d == nil {
		return fmt.Errorf("stuntgres: missing sequence descriptor")
	}
	if d.relationKind != sequenceRelation {
		if d.relationKind == indexRelation {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, d.name), Detail: "This operation is not supported for indexes."}
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, d.name), Detail: "This operation is not supported for tables."}
	}
	return nil
}
func tableKind(d *relationDescriptor, operation string) error {
	if d.relationKind == tableRelation || d.relationKind == partitionedRelation {
		return nil
	}
	if d.relationKind == compositeRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.name), Detail: "This operation is not supported for composite types."}
	}
	if d.relationKind == materializedViewRelation && operation == "change" {
		return sqlError("42809", fmt.Sprintf(`cannot change materialized view "%s"`, d.name))
	}
	if operation == "ADD COLUMN" {
		kind := ""
		switch d.relationKind {
		case viewRelation:
			kind = "views"
		case materializedViewRelation:
			kind = "materialized views"
		case indexRelation:
			kind = "indexes"
		case sequenceRelation:
			kind = "sequences"
		}
		if kind != "" {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action ADD COLUMN cannot be performed on relation "%s"`, d.name), Detail: "This operation is not supported for " + kind + "."}
		}
	}
	if d.relationKind == materializedViewRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.name), Detail: "This operation is not supported for materialized views."}
	}
	if d.relationKind == indexRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.name), Detail: "This operation is not supported for indexes."}
	}
	if d.relationKind == viewRelation {
		return sqlError("0A000", "view mutation is unsupported")
	}
	if d.relationKind == catalogRelation {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	message := fmt.Sprintf(`cannot change sequence "%s"`, d.name)
	detail := ""
	switch operation {
	case "like":
		message = fmt.Sprintf(`relation "%s" is invalid in LIKE clause`, d.name)
		detail = "This operation is not supported for sequences."
	case "comment table":
		message = fmt.Sprintf(`"%s" is not a table`, d.name)
	case "comment column":
		message = fmt.Sprintf(`cannot set comment on relation "%s"`, d.name)
		detail = "This operation is not supported for sequences."
	case "lock":
		message = fmt.Sprintf(`cannot lock relation "%s"`, d.name)
		detail = "This operation is not supported for sequences."
	default:
		if operation != "insert into" && operation != "delete from" && operation != "change" {
			message = fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, operation, d.name)
			detail = "This operation is not supported for sequences."
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: message, Detail: detail}
}
func (s *StatementScope) acquireSequence(ref ObjectRef, mode relationLockMode, reserveReplacement bool) error {
	e := s.op.session.Engine()
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	if err != nil {
		return err
	}
	if reserveReplacement && s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
		return ErrCatalogReplaced
	}
	e.markTemporaryAccessLocked(s, ref)
	return nil
}
func (e *Engine) sequenceColumnsLocked() []ColumnDescriptor {
	view := CatalogRead{e, e.state}
	i, _ := view.LookupType(sqltype.Int8)
	b, _ := view.LookupType(sqltype.Bool)
	return []ColumnDescriptor{{Name: "last_value", Usage: i.Usage(), NullabilityKnown: true}, {Name: "log_cnt", Usage: i.Usage(), NullabilityKnown: true}, {Name: "is_called", Usage: b.Usage(), NullabilityKnown: true}}
}

// Caller holds Engine.mu. Drop it for manager admission and restore it on every
// return; the dependency lock belongs to the statement's transaction ticket.
func (e *Engine) ownedSequenceLocked(scope *StatementScope, auth *AuthorizationView, changes *catalogChanges, d *relationDescriptor, raw parser.ObjectName) (ObjectAddress, error) {
	if len(raw.Parts) == 1 && raw.Parts[0].Text == "none" {
		return ObjectAddress{}, nil
	}
	if len(raw.Parts) < 2 || len(raw.Parts) > 3 {
		return ObjectAddress{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid OWNED BY option", Hint: "Specify OWNED BY table.column or OWNED BY NONE."}
	}
	table := parser.ObjectName{Parts: raw.Parts[:len(raw.Parts)-1]}
	ref, err := e.resolveRelationReference(*auth, table, false)
	if err != nil {
		return ObjectAddress{}, err
	}
	e.mu.Unlock()
	err = scope.acquireSequence(ref, accessShare, true)
	e.mu.Lock()
	if err != nil {
		return ObjectAddress{}, err
	}
	fresh, err := e.authorizationLocked(scope.op)
	if err != nil {
		return ObjectAddress{}, err
	}
	fresh.catalog = e.privateCatalogLocked(changes)
	*auth = fresh
	// Keep the captured incarnation, not a replacement now found by name.
	r, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return ObjectAddress{}, ErrCatalogReplaced
	}
	if r.relationKind != tableRelation && r.relationKind != partitionedRelation {
		return ObjectAddress{}, sqlError("42809", fmt.Sprintf(`referenced relation "%s" is not a table`, r.name))
	}
	if r.owner != d.owner {
		return ObjectAddress{}, sqlError("55000", "sequence must have same owner as table it is linked to")
	}
	if r.namespace != d.namespace {
		return ObjectAddress{}, sqlError("55000", "sequence must be in same schema as table it is linked to")
	}
	name := raw.Parts[len(raw.Parts)-1].Text
	slot, ok := r.index[name]
	if !ok {
		return ObjectAddress{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, r.name))
	}
	a := r.address
	a.SubID = r.attributes[slot]
	return a, nil
}
func sequenceLinkedError(d *relationDescriptor, table *relationDescriptor, move bool) error {
	msg := fmt.Sprintf(`cannot change owner of sequence "%s"`, d.name)
	if move {
		msg = "cannot move an owned sequence into another schema"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: msg, Detail: fmt.Sprintf(`Sequence "%s" is linked to table "%s".`, d.name, table.name)}
}
func (e *Engine) executeSequenceDDL(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	switch s := stmt.(type) {
	case *parser.CreateSequence:
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var options sequenceOptions
		var restart *int64
		// Ordinary CREATE validates options before DefineRelation checks CREATE
		// authority. IF NOT EXISTS checks the namespace before options or skip.
		if !s.IfNotExists {
			options, restart, err = bindSequenceOptions(nil, sequenceGenerator{}, s.Options)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
		changes := cloneCatalogChanges(scope.owner.catalog)
		ns, err := e.relationCreationNamespaceLocked(auth, s.Name, s.Persistence, changes)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		auth.catalog = e.privateCatalogLocked(changes)
		if err = auth.CheckCreate(ns); err != nil && !op.restoreTableDestination {
			e.mu.Unlock()
			return Result{}, err
		}
		persistence, err := adjustRelationCreationPersistence(auth, ns, s.Persistence)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if nsd, ok := auth.catalog.state.descriptors[ns.Address].(*namespaceDescriptor); ok && nsd.temporary == op.session.token {
			scope.owner.accessedTemporary = true
		}
		name := s.Name.Parts[len(s.Name.Parts)-1].Text
		key := NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name}
		if _, ok := auth.catalog.state.names[key]; ok {
			e.mu.Unlock()
			if s.IfNotExists {
				op.existingRelationNotice(name)
				return Result{}, nil
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if s.IfNotExists {
			options, restart, err = bindSequenceOptions(nil, sequenceGenerator{}, s.Options)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
		options.persistence = persistence
		oid, err := e.allocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		rev, err := e.revisionLocked()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		columns := e.sequenceColumnsLocked()
		d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: ns.Address, name: name, revision: rev, sequencePlanStamp: rev, owner: auth.state.CurrentUser, columns: columns, index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, attributes: []int32{1, 2, 3}, relationKind: sequenceRelation, sequence: &options}
		d.attributeHighWater = 3
		for _, option := range s.Options {
			if option.Name == "owned" {
				options.owned, err = e.ownedSequenceLocked(scope, &auth, changes, d, option.Owned)
				if err != nil {
					e.mu.Unlock()
					return Result{}, err
				}
			}
		}
		e.catalogChangeLocked(changes, d.address, d)
		initial := options.start
		if restart != nil {
			initial = *restart
		}
		changes.generators[d.address] = &sequenceGenerator{last: initial, generation: rev}
		scope.catalog = changes
		e.mu.Unlock()
		return Result{}, nil
	case *parser.AlterSequence:
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		ref, err := e.resolveRelationReference(auth, s.Name, false)
		if err != nil {
			e.mu.Unlock()
			if s.IfExists {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42P01" {
					op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, objectNameText(s.Name))})
					return Result{}, nil
				}
			}
			return Result{}, err
		}
		old := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		err = sequenceKind(old)
		if err == nil {
			err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		mode := shareRowExclusive
		if s.Action == "rename" || s.Action == "schema" {
			mode = accessExclusive
		}
		if err = scope.acquireSequence(ref, mode, true); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err = e.authorizationLocked(op)
		if err != nil {
			return Result{}, err
		}
		current, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok {
			return Result{}, ErrCatalogReplaced
		}
		old = current
		if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return Result{}, err
		}
		changes := cloneCatalogChanges(scope.owner.catalog)
		state := e.sequenceGeneratorLocked(scope, ref.Address)
		if state == nil {
			return Result{}, fmt.Errorf("stuntgres: sequence state missing")
		}
		options, restart, err := bindSequenceOptions(old.sequence, *state, s.Options)
		if err != nil {
			return Result{}, err
		}
		rev, err := e.revisionLocked()
		if err != nil {
			return Result{}, err
		}
		d := *old
		d.revision = rev
		d.sequence = &options
		for _, option := range s.Options {
			if option.Name == "owned" {
				if old.sequence.identity {
					a := old.sequence.owned
					a.SubID = 0
					table := auth.catalog.state.descriptors[a].(*relationDescriptor)
					return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot change ownership of identity sequence", Detail: fmt.Sprintf(`Sequence "%s" is linked to table "%s".`, old.name, table.name)}
				}
				options.owned, err = e.ownedSequenceLocked(scope, &auth, changes, &d, option.Owned)
				if err != nil {
					return Result{}, err
				}
			}
		}
		if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return Result{}, err
		}
		switch s.Action {
		case "rename":
			d.name = s.Target.Text
		case "logged", "unlogged":
			if old.sequence.persistence == "temporary" {
				ns := auth.catalog.state.descriptors[old.namespace].(*namespaceDescriptor)
				diagnostic := sqlError("42P16", fmt.Sprintf(`cannot change logged status of table "%s" because it is temporary`, old.name)).(*sqlstate.Error)
				diagnostic.Schema, diagnostic.Table = ns.name, old.name
				return Result{}, diagnostic
			}
			options.persistence = s.Action
		case "owner":
			target, err := schemaOwner(auth, s.Target.Text, s.OwnerIdentity)
			if err != nil {
				return Result{}, err
			}
			if target == d.owner {
				break
			}
			if options.owned != (ObjectAddress{}) {
				a := options.owned
				a.SubID = 0
				return Result{}, sequenceLinkedError(&d, auth.catalog.state.descriptors[a].(*relationDescriptor), false)
			}
			actor, _ := auth.roles.Descriptor(auth.state.CurrentUser)
			if !actor.Superuser {
				role, _ := auth.roles.Descriptor(target)
				if target != actor.Ref {
					return Result{}, sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
				}
				targetAuth := auth
				targetAuth.state.CurrentUser = target
				if err = targetAuth.CheckCreate(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: d.namespace}); err != nil {
					return Result{}, err
				}
			}
			d.owner = target
			d.acl = schemaACLOwner(d.acl, old.owner, d.owner)
		case "schema":
			if options.owned != (ObjectAddress{}) {
				a := options.owned
				a.SubID = 0
				return Result{}, sequenceLinkedError(&d, auth.catalog.state.descriptors[a].(*relationDescriptor), true)
			}
			destination := &parser.CreateSequence{Name: parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.name}}}, Persistence: "logged"}
			ns, err := e.relationCreationNamespaceLocked(auth, destination.Name, destination.Persistence, changes)
			if err != nil {
				return Result{}, err
			}
			// pg_temp may stage a namespace. Resolve permissions and endpoints
			// against that same private catalog, without publishing on rejection.
			auth.catalog = e.privateCatalogLocked(changes)
			if err = auth.CheckCreate(ns); err != nil {
				return Result{}, err
			}
			if _, err = adjustRelationCreationPersistence(auth, ns, destination.Persistence); err != nil {
				return Result{}, err
			}
			oldNamespace := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor)
			newNamespace := auth.catalog.state.descriptors[ns.Address].(*namespaceDescriptor)
			if oldNamespace.temporary != (SessionToken{}) || newNamespace.temporary != (SessionToken{}) {
				return Result{}, sqlError("0A000", "cannot move objects into or out of temporary schemas")
			}
			d.namespace = ns.Address
		}
		rewrite := false
		for _, option := range s.Options {
			rewrite = rewrite || option.Name != "owned"
		}
		persistenceChanged := options.persistence != old.sequence.persistence
		if rewrite || d.name != old.name || d.namespace != old.namespace || d.owner != old.owner || persistenceChanged {
			d.sequencePlanStamp = rev
		}
		key := descriptorNameKey(&d)
		if a, exists := auth.catalog.state.names[key]; exists && a != d.address {
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, d.name))
		}
		e.catalogChangeLocked(changes, d.address, &d)
		if rewrite || persistenceChanged {
			replacement := *state
			replacement.generation = rev
			if restart != nil {
				replacement.last, replacement.called = *restart, false
			}
			changes.generators[d.address] = &replacement
		}
		if s.Action == "" || persistenceChanged {
			if m := op.session.sequences[d.address]; m != nil {
				m.remaining = 0
			}
		}
		scope.catalog = changes
		return Result{}, nil
	case *parser.DropSequence:
		return e.dropSequences(scope, s)
	}
	return Result{}, sqlError("0A000", "unsupported sequence statement")
}
func (e *Engine) dropSequences(scope *StatementScope, s *parser.DropSequence) (Result, error) {
	refs := make([]ObjectRef, 0, len(s.Names))
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	for _, name := range s.Names {
		ref, er := e.resolveRelationReference(auth, name, false)
		if er != nil {
			if d, ok := er.(*sqlstate.Error); s.IfExists && ok && d.Code == "42P01" {
				scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`sequence "%s" does not exist, skipping`, objectNameText(name))})
				continue
			}
			e.mu.Unlock()
			if d, ok := er.(*sqlstate.Error); ok && d.Code == "42P01" {
				return Result{}, sqlError("42P01", fmt.Sprintf(`sequence "%s" does not exist`, objectNameText(name)))
			}
			return Result{}, er
		}
		d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if d.relationKind != sequenceRelation {
			if d.relationKind == indexRelation {
				e.mu.Unlock()
				return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a sequence`, d.name), Hint: "Use DROP INDEX to remove an index."}
			}
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a sequence`, d.name), Hint: "Use DROP TABLE to remove a table."}
		}
		if er = relationDropOwner(auth, ref, d); er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		if d.sequence.identity {
			e.mu.Unlock()
			return Result{}, identitySequenceDropError(auth.catalog, d)
		}
		refs = append(refs, ref)
	}
	e.mu.Unlock()
	for _, ref := range refs {
		if err = scope.acquireSequence(ref, accessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	auth, err = e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	targets := make([]ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
	}
	// Every stored-expression owner shares one private dependency closure.
	checkStage := e.BeginCatalogStage(auth.catalog)
	checkStage.authSession, checkStage.authRevision, checkStage.roleRevision = scope.op.session, auth.revision, auth.roles.state.revision
	defer e.DiscardCatalogStage(checkStage)
	checkStage.dependencyPath, err = e.referencePath(auth)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	e.mu.Unlock()
	if err := scope.stageExpressionDependents(checkStage, targets, s.Mode, changes); err != nil {
		return Result{}, err
	}
	if _, err := e.ValidateCatalogStage(scope.op.Context(), checkStage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if auth.catalog.Epoch() != e.state.epoch {
		return Result{}, ErrCatalogReplaced
	}
	for address := range checkStage.descriptors {
		expected := auth.catalog.state.descriptors[address]
		if original, private := changes.originals[address]; private {
			expected = original
		}
		if e.state.descriptors[address] != expected {
			return Result{}, CatalogConflict{}
		}
	}
	for address := range checkStage.removals {
		expected := auth.catalog.state.descriptors[address]
		if original, private := changes.originals[address]; private {
			expected = original
		}
		if e.state.descriptors[address] != expected {
			return Result{}, CatalogConflict{}
		}
	}
	for address, descriptor := range checkStage.descriptors {
		e.catalogChangeLocked(changes, address, descriptor)
	}
	for address := range checkStage.removals {
		e.catalogChangeLocked(changes, address, nil)
	}
	cascade := checkStage.viewCascadeNames
	for _, ref := range refs {
		d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok {
			return Result{}, ErrCatalogReplaced
		}
		if err = relationDropOwner(auth, ref, d); err != nil {
			return Result{}, err
		}
		if d.sequence.identity {
			return Result{}, identitySequenceDropError(auth.catalog, d)
		}
		e.catalogChangeLocked(changes, ref.Address, nil)
		delete(changes.generators, ref.Address)
	}
	for address, descriptor := range checkStage.descriptors {
		next, ok := descriptor.(*relationDescriptor)
		old, oldOK := auth.catalog.state.descriptors[address].(*relationDescriptor)
		if !ok || !oldOK {
			continue
		}
		for _, attribute := range old.attributes {
			if !next.hasAttribute(attribute) {
				target := address
				target.SubID = attribute
				targets = append(targets, target)
			}
		}
	}
	for address := range checkStage.removals {
		targets = append(targets, address)
	}
	changes.dependencies = append(changes.dependencies, catalogDependencyObservation{base: auth.catalog.state, targets: targets})
	scope.catalog = changes
	emitViewCascade(scope.op, cascade)
	return Result{}, nil
}
func relationDropOwner(auth AuthorizationView, ref ObjectRef, d *relationDescriptor) error {
	err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	if err == nil {
		return nil
	}
	if _, validationErr := auth.object(ref); validationErr != nil {
		return validationErr
	}
	ns, ok := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: invalid sequence namespace")
	}
	namespace := ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: ns.address}
	if auth.Check(AccessRequirement{Object: namespace, OwnerOnly: true}) == nil {
		return nil
	}
	return err
}
