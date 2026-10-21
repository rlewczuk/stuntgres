package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (v AuthorizationView) checkSequenceAny(ref ids.ObjectRef, mask authorization.Privileges) error {
	d, err := v.Object(ref)
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
func (s *StatementScope) acquireSequence(ref ids.ObjectRef, mode lock.Mode, reserveReplacement bool) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
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

// Caller holds Engine.mu. Drop it for manager admission and restore it on every
// return; the dependency lock belongs to the statement's transaction ticket.
func (e *Engine) ownedSequenceLocked(scope *StatementScope, auth *AuthorizationView, changes *session.Journal, d *catalog.Relation, raw parser.ObjectName) (ids.ObjectAddress, error) {
	table, column, none, err := sequence.ParseOwnedBy(raw)
	if err != nil || none {
		return ids.ObjectAddress{}, err
	}
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
	fresh.Catalog = e.privateCatalogLocked(changes)
	fresh.Recapture(fresh.Catalog)
	*auth = fresh
	// Keep the captured incarnation, not a replacement now found by name.
	r, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ObjectAddress{}, ids.ErrCatalogReplaced
	}
	return sequence.CheckOwnedBy(r, d, column)
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
			options, restart, err = sequence.BindOptions(nil, storage.SequenceGenerator{}, s.Options)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
		changes := scope.owner.Catalog.Clone()
		ns, err := e.relationCreationNamespaceLocked(auth, s.Name, s.Persistence, changes)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		auth.Catalog = e.privateCatalogLocked(changes)
		auth.Recapture(auth.Catalog)
		if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
			auth.stagedTemporary = table.Address()
		}
		if err = auth.CheckCreate(ns); err != nil && !operationReplayOrZero(op).tableDestination {
			e.mu.Unlock()
			return Result{}, err
		}
		persistence, err := adjustRelationCreationPersistence(auth, ns, s.Persistence)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if nsd, ok := auth.Catalog.Namespace(ns.Address); ok && nsd.Temporary == op.Session.SessionToken {
			scope.owner.AccessedTemporary = true
		}
		name := s.Name.Parts[len(s.Name.Parts)-1].Text
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}
		if _, ok := auth.Catalog.LookupName(key); ok {
			e.mu.Unlock()
			if s.IfNotExists {
				op.ExistingRelationNotice(name)
				return Result{}, nil
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if s.IfNotExists {
			options, restart, err = sequence.BindOptions(nil, storage.SequenceGenerator{}, s.Options)
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
		d := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, Namespace: ns.Address, Name: name, Revision: rev, Owner: auth.State.CurrentUser, Columns: sequence.Columns(e.catalog.read(e.catalog.state)), Options: options})
		d.Security.ACL = e.creationACLChangesLocked(changes, auth.Catalog, d.Owner, d.Namespace(), authorization.DefaultSequences)
		// OWNED BY may refresh auth after waiting for its table. Retain the
		// authority that actually passed the earlier namespace CREATE check.
		scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
		for _, option := range s.Options {
			if option.Name == "owned" {
				d.Sequence.Owned, err = e.ownedSequenceLocked(scope, &auth, changes, d, option.Owned)
				if err != nil {
					e.mu.Unlock()
					return Result{}, err
				}
			}
		}
		e.catalogChangeLocked(changes, d.Address(), d)
		changes.Generators[d.Address()] = sequence.InitialGenerator(*d.Sequence, restart, rev)
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
					op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, catalog.ObjectNameText(s.Name))})
					return Result{}, nil
				}
			}
			return Result{}, err
		}
		old := auth.Catalog.MustRelation(ref.Address)
		err = sequence.CheckKind(old)
		if err == nil {
			err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
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
		current, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return Result{}, ids.ErrCatalogReplaced
		}
		old = current
		if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return Result{}, err
		}
		var ownerTarget authorization.PrincipalRef
		if s.Action == "owner" {
			ownerTarget, err = access.SchemaOwner(auth.View, s.Target.Text, s.OwnerIdentity)
			if err != nil {
				return Result{}, err
			}
			if ownerTarget == old.Owner {
				return Result{}, op.Context().Err()
			}
		}
		changes := scope.owner.Catalog.Clone()
		state := e.sequenceGeneratorLocked(scope, ref.Address)
		if state == nil {
			return Result{}, fmt.Errorf("stuntgres: sequence state missing")
		}
		options, restart, err := sequence.BindOptions(old.Sequence, *state, s.Options)
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
					return Result{}, sequence.IdentityOwnershipError(old, auth.Catalog.MustRelation(a))
				}
				options.Owned, err = e.ownedSequenceLocked(scope, &auth, changes, &d, option.Owned)
				if err != nil {
					return Result{}, err
				}
			}
		}
		if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return Result{}, err
		}
		switch s.Action {
		case "rename":
			d.ObjectName = s.Target.Text
		case "logged", "unlogged":
			if old.Sequence.Persistence == "temporary" {
				return Result{}, sequence.TemporaryLoggedError(old, auth.Catalog.MustNamespace(old.Namespace()).Name())
			}
			options.Persistence = s.Action
		case "owner":
			target := ownerTarget
			if options.Owned != (ids.ObjectAddress{}) {
				a := options.Owned
				a.SubID = 0
				return Result{}, sequence.LinkedError(&d, auth.Catalog.MustRelation(a), false)
			}
			actor, _ := auth.Roles.Descriptor(auth.State.CurrentUser)
			if !actor.Superuser {
				role, _ := auth.Roles.Descriptor(target)
				allowed, err := auth.Roles.CanSetRole(actor.Ref, target)
				if err != nil {
					return Result{}, err
				}
				if !allowed {
					return Result{}, sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
				}
				targetAuth := auth
				targetAuth.State.CurrentUser = target
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
				return Result{}, sequence.LinkedError(&d, auth.Catalog.MustRelation(a), true)
			}
			destination := &parser.CreateSequence{Name: parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}}, Persistence: "logged"}
			ns, err := e.relationCreationNamespaceLocked(auth, destination.Name, destination.Persistence, changes)
			if err != nil {
				return Result{}, err
			}
			// pg_temp may stage a namespace. Resolve permissions and endpoints
			// against that same private catalog, without publishing on rejection.
			auth.Catalog = e.privateCatalogLocked(changes)
			auth.Recapture(auth.Catalog)
			if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
				auth.stagedTemporary = table.Address()
			}
			if err = auth.CheckCreate(ns); err != nil {
				return Result{}, err
			}
			if _, err = adjustRelationCreationPersistence(auth, ns, destination.Persistence); err != nil {
				return Result{}, err
			}
			if err = sequence.CheckSchemaMove(auth.Catalog.MustNamespace(d.Namespace()), auth.Catalog.MustNamespace(ns.Address)); err != nil {
				return Result{}, err
			}
			d.ObjectNamespace = ns.Address
		}
		effects := sequence.AlterEffects(old, &d, s.Options, s.Action)
		if effects.PlanStamp {
			d.SequencePlanStamp = rev
		}
		key := catalog.DescriptorNameKey(&d)
		if a, exists := auth.Catalog.LookupName(key); exists && a != d.Address() {
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, d.Name()))
		}
		e.catalogChangeLocked(changes, d.Address(), &d)
		if effects.ReplaceGenerator {
			changes.Generators[d.Address()] = sequence.ReplacementGenerator(*state, rev, restart)
		}
		if effects.DiscardTail {
			if m := payloadOf(op.Session).sequences.Memory(d.Address()); m != nil {
				m.DiscardTailLocked()
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
				scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`sequence "%s" does not exist, skipping`, catalog.ObjectNameText(name))})
				continue
			}
			e.mu.Unlock()
			if d, ok := er.(*sqlstate.Error); ok && d.Code == "42P01" {
				return Result{}, sqlError("42P01", fmt.Sprintf(`sequence "%s" does not exist`, catalog.ObjectNameText(name)))
			}
			return Result{}, er
		}
		d := auth.Catalog.MustRelation(ref.Address)
		if d.RelationKind != catalog.SequenceRelation {
			e.mu.Unlock()
			return Result{}, sequence.NotSequenceError(d)
		}
		if er = index.RelationDropOwner(auth.View, auth, ref, d); er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		if d.Sequence.Identity {
			e.mu.Unlock()
			return Result{}, sequence.IdentityDropError(auth.Catalog, d)
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
	changes := scope.owner.Catalog.Clone()
	targets := make([]ids.ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
	}
	// Every stored-expression owner shares one private dependency closure.
	checkStage := e.BeginCatalogStage(auth.Catalog)
	checkStage.authorizeLocked(auth, scope.op.Session)
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
	if auth.Catalog.Epoch() != e.catalog.state.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	for address := range checkStage.Descriptors() {
		expected := auth.Catalog.Get(address)
		if original, private := changes.Originals[address]; private {
			expected = original
		}
		if e.catalog.state.Get(address) != expected {
			return Result{}, catalog.Conflict{}
		}
	}
	for address := range checkStage.Removals() {
		expected := auth.Catalog.Get(address)
		if original, private := changes.Originals[address]; private {
			expected = original
		}
		if e.catalog.state.Get(address) != expected {
			return Result{}, catalog.Conflict{}
		}
	}
	for address, descriptor := range checkStage.Descriptors() {
		e.catalogChangeLocked(changes, address, descriptor)
	}
	for address := range checkStage.Removals() {
		e.catalogChangeLocked(changes, address, nil)
	}
	cascade := checkStage.dropCascadeNames
	for _, ref := range refs {
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return Result{}, ids.ErrCatalogReplaced
		}
		if err = index.RelationDropOwner(auth.View, auth, ref, d); err != nil {
			return Result{}, err
		}
		if d.Sequence.Identity {
			return Result{}, sequence.IdentityDropError(auth.Catalog, d)
		}
		e.catalogChangeLocked(changes, ref.Address, nil)
		delete(changes.Generators, ref.Address)
	}
	for address, descriptor := range checkStage.Descriptors() {
		next, ok := descriptor.(*catalog.Relation)
		old, oldOK := auth.Catalog.Relation(address)
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
	for address := range checkStage.Removals() {
		targets = append(targets, address)
	}
	changes.Dependencies = append(changes.Dependencies, session.DependencyObservation{Base: auth.Catalog.State(), Targets: targets})
	scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	scope.catalog = changes
	emitViewCascade(scope.op, cascade)
	return Result{}, nil
}
