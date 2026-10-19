package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (v AuthorizationView) checkSequenceAny(ref ids.ObjectRef, mask authorization.Privileges) error {
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
func sequenceKind(d *catalog.Relation) error {
	if d == nil {
		return fmt.Errorf("stuntgres: missing sequence descriptor")
	}
	if d.RelationKind != catalog.SequenceRelation {
		if d.RelationKind == catalog.IndexRelation {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, d.Name()), Detail: "This operation is not supported for indexes."}
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, d.Name()), Detail: "This operation is not supported for tables."}
	}
	return nil
}
func tableKind(d *catalog.Relation, operation string) error {
	if d.RelationKind == catalog.TableRelation || d.RelationKind == catalog.PartitionedRelation {
		return nil
	}
	if d.RelationKind == catalog.CompositeRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.Name()), Detail: "This operation is not supported for composite types."}
	}
	if d.RelationKind == catalog.MaterializedViewRelation && operation == "change" {
		return sqlError("42809", fmt.Sprintf(`cannot change materialized view "%s"`, d.Name()))
	}
	if operation == "ADD COLUMN" {
		kind := ""
		switch d.RelationKind {
		case catalog.ViewRelation:
			kind = "views"
		case catalog.MaterializedViewRelation:
			kind = "materialized views"
		case catalog.IndexRelation:
			kind = "indexes"
		case catalog.SequenceRelation:
			kind = "sequences"
		}
		if kind != "" {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action ADD COLUMN cannot be performed on relation "%s"`, d.Name()), Detail: "This operation is not supported for " + kind + "."}
		}
	}
	if d.RelationKind == catalog.MaterializedViewRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.Name()), Detail: "This operation is not supported for materialized views."}
	}
	if d.RelationKind == catalog.IndexRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.Name()), Detail: "This operation is not supported for indexes."}
	}
	if d.RelationKind == catalog.ViewRelation {
		return sqlError("0A000", "view mutation is unsupported")
	}
	if d.RelationKind == catalog.CatalogRelation {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	message := fmt.Sprintf(`cannot change sequence "%s"`, d.Name())
	detail := ""
	switch operation {
	case "like":
		message = fmt.Sprintf(`relation "%s" is invalid in LIKE clause`, d.Name())
		detail = "This operation is not supported for sequences."
	case "comment table":
		message = fmt.Sprintf(`"%s" is not a table`, d.Name())
	case "comment column":
		message = fmt.Sprintf(`cannot set comment on relation "%s"`, d.Name())
		detail = "This operation is not supported for sequences."
	case "lock":
		message = fmt.Sprintf(`cannot lock relation "%s"`, d.Name())
		detail = "This operation is not supported for sequences."
	default:
		if operation != "insert into" && operation != "delete from" && operation != "change" {
			message = fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, operation, d.Name())
			detail = "This operation is not supported for sequences."
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: message, Detail: detail}
}
func (s *StatementScope) acquireSequence(ref ids.ObjectRef, mode lock.Mode, reserveReplacement bool) error {
	e := s.op.session.Engine()
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
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
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	e.markTemporaryAccessLocked(s, ref)
	return nil
}
func (e *Engine) sequenceColumnsLocked() []catalog.Column {
	view := e.catalog.read(e.catalog.state)
	i, _ := view.LookupType(sqltype.Int8)
	b, _ := view.LookupType(sqltype.Bool)
	return []catalog.Column{{Name: "last_value", Usage: i.Usage(), NullabilityKnown: true}, {Name: "log_cnt", Usage: i.Usage(), NullabilityKnown: true}, {Name: "is_called", Usage: b.Usage(), NullabilityKnown: true}}
}

// Caller holds Engine.mu. Drop it for manager admission and restore it on every
// return; the dependency lock belongs to the statement's transaction ticket.
func (e *Engine) ownedSequenceLocked(scope *StatementScope, auth *AuthorizationView, changes *catalogChanges, d *catalog.Relation, raw parser.ObjectName) (ids.ObjectAddress, error) {
	if len(raw.Parts) == 1 && raw.Parts[0].Text == "none" {
		return ids.ObjectAddress{}, nil
	}
	if len(raw.Parts) < 2 || len(raw.Parts) > 3 {
		return ids.ObjectAddress{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid OWNED BY option", Hint: "Specify OWNED BY table.column or OWNED BY NONE."}
	}
	table := parser.ObjectName{Parts: raw.Parts[:len(raw.Parts)-1]}
	ref, err := e.resolveRelationReference(*auth, table, false)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	e.mu.Unlock()
	err = scope.acquireSequence(ref, lock.AccessShare, true)
	e.mu.Lock()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	fresh, err := e.authorizationLocked(scope.op)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	fresh.catalog = e.privateCatalogLocked(changes)
	fresh.recaptureCatalog(fresh.catalog)
	*auth = fresh
	// Keep the captured incarnation, not a replacement now found by name.
	r, ok := auth.catalog.Relation(ref.Address)
	if !ok {
		return ids.ObjectAddress{}, ids.ErrCatalogReplaced
	}
	if r.RelationKind != catalog.TableRelation && r.RelationKind != catalog.PartitionedRelation {
		return ids.ObjectAddress{}, sqlError("42809", fmt.Sprintf(`referenced relation "%s" is not a table`, r.Name()))
	}
	if r.Owner != d.Owner {
		return ids.ObjectAddress{}, sqlError("55000", "sequence must have same owner as table it is linked to")
	}
	if r.Namespace() != d.Namespace() {
		return ids.ObjectAddress{}, sqlError("55000", "sequence must be in same schema as table it is linked to")
	}
	name := raw.Parts[len(raw.Parts)-1].Text
	slot, ok := r.Index[name]
	if !ok {
		return ids.ObjectAddress{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, r.Name()))
	}
	a := r.Address()
	a.SubID = r.Attributes[slot]
	return a, nil
}
func sequenceLinkedError(d *catalog.Relation, table *catalog.Relation, move bool) error {
	msg := fmt.Sprintf(`cannot change owner of sequence "%s"`, d.Name())
	if move {
		msg = "cannot move an owned sequence into another schema"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: msg, Detail: fmt.Sprintf(`Sequence "%s" is linked to table "%s".`, d.Name(), table.Name())}
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
		var options catalog.SequenceOptions
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
		auth.recaptureCatalog(auth.catalog)
		if table, ok := changes.descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.session.token {
			auth.stagedTemporary = table.Address()
		}
		if err = auth.CheckCreate(ns); err != nil && !op.restoreTableDestination {
			e.mu.Unlock()
			return Result{}, err
		}
		persistence, err := adjustRelationCreationPersistence(auth, ns, s.Persistence)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if nsd, ok := auth.catalog.Namespace(ns.Address); ok && nsd.Temporary == op.session.token {
			scope.owner.accessedTemporary = true
		}
		name := s.Name.Parts[len(s.Name.Parts)-1].Text
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}
		if _, ok := auth.catalog.LookupName(key); ok {
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
		options.Persistence = persistence
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		columns := e.sequenceColumnsLocked()
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns.Address, ObjectName: name, ObjectRevision: rev, SequencePlanStamp: rev, Owner: auth.state.CurrentUser, Columns: columns, Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
		d.Security.ACL = e.creationACLChangesLocked(changes, auth.catalog, d.Owner, d.Namespace(), authorization.DefaultSequences)
		d.ColumnMeta.HighWater = 3
		// OWNED BY may refresh auth after waiting for its table. Retain the
		// authority that actually passed the earlier namespace CREATE check.
		scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
		for _, option := range s.Options {
			if option.Name == "owned" {
				options.Owned, err = e.ownedSequenceLocked(scope, &auth, changes, d, option.Owned)
				if err != nil {
					e.mu.Unlock()
					return Result{}, err
				}
			}
		}
		e.catalogChangeLocked(changes, d.Address(), d)
		initial := options.Start
		if restart != nil {
			initial = *restart
		}
		changes.generators[d.Address()] = &sequenceGenerator{Last: initial, Generation: rev}
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
		old := auth.catalog.MustRelation(ref.Address)
		err = sequenceKind(old)
		if err == nil {
			err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		mode := lock.ShareRowExclusive
		if s.Action == "rename" || s.Action == "schema" {
			mode = lock.AccessExclusive
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
		current, ok := auth.catalog.Relation(ref.Address)
		if !ok {
			return Result{}, ids.ErrCatalogReplaced
		}
		old = current
		if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return Result{}, err
		}
		var ownerTarget authorization.PrincipalRef
		if s.Action == "owner" {
			ownerTarget, err = schemaOwner(auth, s.Target.Text, s.OwnerIdentity)
			if err != nil {
				return Result{}, err
			}
			if ownerTarget == old.Owner {
				return Result{}, op.Context().Err()
			}
		}
		changes := cloneCatalogChanges(scope.owner.catalog)
		state := e.sequenceGeneratorLocked(scope, ref.Address)
		if state == nil {
			return Result{}, fmt.Errorf("stuntgres: sequence state missing")
		}
		options, restart, err := bindSequenceOptions(old.Sequence, *state, s.Options)
		if err != nil {
			return Result{}, err
		}
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			return Result{}, err
		}
		d := *old
		d.ObjectRevision = rev
		d.Sequence = &options
		for _, option := range s.Options {
			if option.Name == "owned" {
				if old.Sequence.Identity {
					a := old.Sequence.Owned
					a.SubID = 0
					table := auth.catalog.MustRelation(a)
					return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot change ownership of identity sequence", Detail: fmt.Sprintf(`Sequence "%s" is linked to table "%s".`, old.Name(), table.Name())}
				}
				options.Owned, err = e.ownedSequenceLocked(scope, &auth, changes, &d, option.Owned)
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
			d.ObjectName = s.Target.Text
		case "logged", "unlogged":
			if old.Sequence.Persistence == "temporary" {
				ns := auth.catalog.MustNamespace(old.Namespace())
				diagnostic := sqlError("42P16", fmt.Sprintf(`cannot change logged status of table "%s" because it is temporary`, old.Name())).(*sqlstate.Error)
				diagnostic.Schema, diagnostic.Table = ns.Name(), old.Name()
				return Result{}, diagnostic
			}
			options.Persistence = s.Action
		case "owner":
			target := ownerTarget
			if options.Owned != (ids.ObjectAddress{}) {
				a := options.Owned
				a.SubID = 0
				return Result{}, sequenceLinkedError(&d, auth.catalog.MustRelation(a), false)
			}
			actor, _ := auth.roles.Descriptor(auth.state.CurrentUser)
			if !actor.Superuser {
				role, _ := auth.roles.Descriptor(target)
				allowed, err := auth.roles.CanSetRole(actor.Ref, target)
				if err != nil {
					return Result{}, err
				}
				if !allowed {
					return Result{}, sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
				}
				targetAuth := auth
				targetAuth.state.CurrentUser = target
				if err = targetAuth.CheckCreate(e.catalog.Domain.Ref(ref.Epoch, d.Namespace())); err != nil {
					return Result{}, err
				}
			}
			d.Owner = target
			d.Security.ACL = authorization.SchemaACLOwner(d.Security.ACL, old.Owner, d.Owner)
		case "schema":
			if options.Owned != (ids.ObjectAddress{}) {
				a := options.Owned
				a.SubID = 0
				return Result{}, sequenceLinkedError(&d, auth.catalog.MustRelation(a), true)
			}
			destination := &parser.CreateSequence{Name: parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}}, Persistence: "logged"}
			ns, err := e.relationCreationNamespaceLocked(auth, destination.Name, destination.Persistence, changes)
			if err != nil {
				return Result{}, err
			}
			// pg_temp may stage a namespace. Resolve permissions and endpoints
			// against that same private catalog, without publishing on rejection.
			auth.catalog = e.privateCatalogLocked(changes)
			auth.recaptureCatalog(auth.catalog)
			if table, ok := changes.descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.session.token {
				auth.stagedTemporary = table.Address()
			}
			if err = auth.CheckCreate(ns); err != nil {
				return Result{}, err
			}
			if _, err = adjustRelationCreationPersistence(auth, ns, destination.Persistence); err != nil {
				return Result{}, err
			}
			oldNamespace := auth.catalog.MustNamespace(d.Namespace())
			newNamespace := auth.catalog.MustNamespace(ns.Address)
			if oldNamespace.Temporary != (ids.SessionToken{}) || newNamespace.Temporary != (ids.SessionToken{}) {
				return Result{}, sqlError("0A000", "cannot move objects into or out of temporary schemas")
			}
			d.ObjectNamespace = ns.Address
		}
		rewrite := false
		for _, option := range s.Options {
			rewrite = rewrite || option.Name != "owned"
		}
		persistenceChanged := options.Persistence != old.Sequence.Persistence
		if rewrite || d.Name() != old.Name() || d.Namespace() != old.Namespace() || d.Owner != old.Owner || persistenceChanged {
			d.SequencePlanStamp = rev
		}
		key := catalog.DescriptorNameKey(&d)
		if a, exists := auth.catalog.LookupName(key); exists && a != d.Address() {
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, d.Name()))
		}
		e.catalogChangeLocked(changes, d.Address(), &d)
		if rewrite || persistenceChanged {
			replacement := *state
			replacement.Generation = rev
			if restart != nil {
				replacement.Last, replacement.Called = *restart, false
			}
			changes.generators[d.Address()] = &replacement
		}
		if s.Action == "" || persistenceChanged {
			if m := op.session.sequences[d.Address()]; m != nil {
				m.remaining = 0
			}
		}
		scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
		scope.catalog = changes
		return Result{}, nil
	case *parser.DropSequence:
		return e.dropSequences(scope, s)
	}
	return Result{}, sqlError("0A000", "unsupported sequence statement")
}
func (e *Engine) dropSequences(scope *StatementScope, s *parser.DropSequence) (Result, error) {
	refs := make([]ids.ObjectRef, 0, len(s.Names))
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
		d := auth.catalog.MustRelation(ref.Address)
		if d.RelationKind != catalog.SequenceRelation {
			if d.RelationKind == catalog.IndexRelation {
				e.mu.Unlock()
				return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a sequence`, d.Name()), Hint: "Use DROP INDEX to remove an index."}
			}
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a sequence`, d.Name()), Hint: "Use DROP TABLE to remove a table."}
		}
		if er = relationDropOwner(auth, ref, d); er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		if d.Sequence.Identity {
			e.mu.Unlock()
			return Result{}, identitySequenceDropError(auth.catalog, d)
		}
		refs = append(refs, ref)
	}
	e.mu.Unlock()
	for _, ref := range refs {
		if err = scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
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
	targets := make([]ids.ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
	}
	// Every stored-expression owner shares one private dependency closure.
	checkStage := e.BeginCatalogStage(auth.catalog)
	checkStage.authorizeLocked(auth, scope.op.session)
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
	if auth.catalog.Epoch() != e.catalog.state.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	for address := range checkStage.Descriptors {
		expected := auth.catalog.Get(address)
		if original, private := changes.originals[address]; private {
			expected = original
		}
		if e.catalog.state.Get(address) != expected {
			return Result{}, catalog.Conflict{}
		}
	}
	for address := range checkStage.Removals {
		expected := auth.catalog.Get(address)
		if original, private := changes.originals[address]; private {
			expected = original
		}
		if e.catalog.state.Get(address) != expected {
			return Result{}, catalog.Conflict{}
		}
	}
	for address, descriptor := range checkStage.Descriptors {
		e.catalogChangeLocked(changes, address, descriptor)
	}
	for address := range checkStage.Removals {
		e.catalogChangeLocked(changes, address, nil)
	}
	cascade := checkStage.viewCascadeNames
	for _, ref := range refs {
		d, ok := auth.catalog.Relation(ref.Address)
		if !ok {
			return Result{}, ids.ErrCatalogReplaced
		}
		if err = relationDropOwner(auth, ref, d); err != nil {
			return Result{}, err
		}
		if d.Sequence.Identity {
			return Result{}, identitySequenceDropError(auth.catalog, d)
		}
		e.catalogChangeLocked(changes, ref.Address, nil)
		delete(changes.generators, ref.Address)
	}
	for address, descriptor := range checkStage.Descriptors {
		next, ok := descriptor.(*catalog.Relation)
		old, oldOK := auth.catalog.Relation(address)
		if !ok || !oldOK {
			continue
		}
		for _, attribute := range old.Attributes {
			if !next.HasAttribute(attribute) {
				target := address
				target.SubID = attribute
				targets = append(targets, target)
			}
		}
	}
	for address := range checkStage.Removals {
		targets = append(targets, address)
	}
	changes.dependencies = append(changes.dependencies, catalogDependencyObservation{base: auth.catalog.State(), targets: targets})
	scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	scope.catalog = changes
	emitViewCascade(scope.op, cascade)
	return Result{}, nil
}
func relationDropOwner(auth AuthorizationView, ref ids.ObjectRef, d *catalog.Relation) error {
	err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	if err == nil {
		return nil
	}
	if _, validationErr := auth.object(ref); validationErr != nil {
		return validationErr
	}
	ns, ok := auth.catalog.Namespace(d.Namespace())
	if !ok {
		return fmt.Errorf("stuntgres: invalid sequence namespace")
	}
	namespace := ref.Domain().Ref(ref.Epoch, ns.Address())
	if auth.Check(AccessRequirement{Object: namespace, OwnerOnly: true}) == nil {
		return nil
	}
	return err
}
