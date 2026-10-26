package tablecmds

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Caller holds Engine.mu. Drop it for manager admission and restore it on every
// return; the dependency lock belongs to the statement's transaction ticket.
func ownedSequenceLocked(e *core.Engine, scope *core.StatementScope, auth *core.AuthorizationView, changes *session.Journal, d *catalog.Relation, raw parser.ObjectName) (ids.ObjectAddress, error) {
	table, column, none, err := sequence.ParseOwnedBy(raw)
	if err != nil || none {
		return ids.ObjectAddress{}, err
	}
	ref, err := e.ResolveRelationReference(*auth, table, false)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	e.Mu.Unlock()
	err = scope.AcquireSequence(ref, lock.AccessShare, true)
	e.Mu.Lock()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	fresh, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	fresh.Catalog = e.PrivateCatalogLocked(changes)
	fresh.Recapture(fresh.Catalog)
	*auth = fresh
	// Keep the captured incarnation, not a replacement now found by name.
	r, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ObjectAddress{}, ids.ErrCatalogReplaced
	}
	return sequence.CheckOwnedBy(r, d, column)
}

// ExecuteSequenceDDL runs CREATE, ALTER and DROP SEQUENCE in the statement
// scope; it takes Engine.Mu itself and releases it around lock waits.
func ExecuteSequenceDDL(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	switch s := stmt.(type) {
	case *parser.CreateSequence:
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		var options catalog.SequenceOptions
		var restart *int64
		// Ordinary CREATE validates options before DefineRelation checks CREATE
		// authority. IF NOT EXISTS checks the namespace before options or skip.
		if !s.IfNotExists {
			options, restart, err = sequence.BindOptions(nil, storage.SequenceGenerator{}, s.Options)
			if err != nil {
				e.Mu.Unlock()
				return core.Result{}, err
			}
		}
		changes := scope.PendingCatalog().Clone()
		ns, err := e.RelationCreationNamespaceLocked(auth, s.Name, s.Persistence, changes)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		auth.Catalog = e.PrivateCatalogLocked(changes)
		auth.Recapture(auth.Catalog)
		if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
			auth.StagedTemporary = table.Address()
		}
		if err = auth.CheckCreate(ns); err != nil && !core.OperationReplayOrZero(op).TableDestination {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		persistence, err := access.AdjustRelationCreationPersistence(auth.View, auth, ns, s.Persistence)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		if nsd, ok := auth.Catalog.Namespace(ns.Address); ok && nsd.Temporary == op.Session.SessionToken {
			scope.Owner.AccessedTemporary = true
		}
		name := s.Name.Parts[len(s.Name.Parts)-1].Text
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}
		if _, ok := auth.Catalog.LookupName(key); ok {
			e.Mu.Unlock()
			if s.IfNotExists {
				op.ExistingRelationNotice(name)
				return core.Result{}, nil
			}
			return core.Result{}, core.SQLError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if s.IfNotExists {
			options, restart, err = sequence.BindOptions(nil, storage.SequenceGenerator{}, s.Options)
			if err != nil {
				e.Mu.Unlock()
				return core.Result{}, err
			}
		}
		options.Persistence = persistence
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		rev, err := e.Catalog.RevisionLocked()
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		d := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, Namespace: ns.Address, Name: name, Revision: rev, Owner: auth.State.CurrentUser, Columns: sequence.Columns(e.Catalog.Current()), Options: options})
		d.Security.ACL = e.CreationACLChangesLocked(changes, auth.Catalog, d.Owner, d.Namespace(), authorization.DefaultSequences)
		// OWNED BY may refresh auth after waiting for its table. Retain the
		// authority that actually passed the earlier namespace CREATE check.
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
		for _, option := range s.Options {
			if option.Name == "owned" {
				d.Sequence.Owned, err = ownedSequenceLocked(e, scope, &auth, changes, d, option.Owned)
				if err != nil {
					e.Mu.Unlock()
					return core.Result{}, err
				}
			}
		}
		e.CatalogChangeLocked(changes, d.Address(), d)
		changes.Generators[d.Address()] = sequence.InitialGenerator(*d.Sequence, restart, rev)
		scope.Catalog = changes
		e.Mu.Unlock()
		if persistence != "temporary" {
			if err := scope.AcquireCatalogNamespace(auth.Catalog, ns.Address, s.Name); err != nil {
				return core.Result{}, err
			}
			if err := acquireCatalogBinding(scope, auth.Catalog, catalog.BindingClaim{Key: key, Address: d.Address(), Priority: 1}); err != nil {
				if s.IfNotExists {
					skipped, failure := scope.SkipConcurrentRelation(auth.Catalog, key, s.Name, err)
					if skipped {
						scope.Catalog = nil
						return core.Result{}, nil
					}
					return core.Result{}, failure
				}
				return core.Result{}, err
			}
		}
		return core.Result{}, nil
	case *parser.AlterSequence:
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		ref, err := e.ResolveRelationReference(auth, s.Name, false)
		if err != nil {
			e.Mu.Unlock()
			if s.IfExists {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42P01" {
					op.EmitNotice(session.LevelNotice, session.MissingRelation(catalog.ObjectNameText(s.Name)))
					return core.Result{}, nil
				}
			}
			return core.Result{}, err
		}
		old := auth.Catalog.MustRelation(ref.Address)
		err = sequence.CheckKind(old)
		if err == nil {
			err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
		}
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		mode := lock.ShareRowExclusive
		if s.Action == "rename" || s.Action == "schema" {
			mode = lock.AccessExclusive
		}
		if err = scope.AcquireSequence(ref, mode, true); err != nil {
			return core.Result{}, err
		}
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err = e.AuthorizationLocked(op)
		if err != nil {
			return core.Result{}, err
		}
		current, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return core.Result{}, ids.ErrCatalogReplaced
		}
		old = current
		if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return core.Result{}, err
		}
		var ownerTarget authorization.PrincipalRef
		if s.Action == "owner" {
			ownerTarget, err = access.SchemaOwner(auth.View, s.Target.Text, s.OwnerIdentity)
			if err != nil {
				return core.Result{}, err
			}
			if ownerTarget == old.Owner {
				return core.Result{}, op.Context().Err()
			}
		}
		changes := scope.PendingCatalog().Clone()
		state := e.SequenceGeneratorLocked(scope, ref.Address)
		if state == nil {
			return core.Result{}, fmt.Errorf("stuntgres: sequence state missing")
		}
		options, restart, err := sequence.BindOptions(old.Sequence, *state, s.Options)
		if err != nil {
			return core.Result{}, err
		}
		rev, err := e.Catalog.RevisionLocked()
		if err != nil {
			return core.Result{}, err
		}
		d := *old
		d.ObjectRevision = rev
		d.Sequence = &options
		for _, option := range s.Options {
			if option.Name == "owned" {
				if old.Sequence.Identity {
					a := old.Sequence.Owned
					a.SubID = 0
					return core.Result{}, sequence.IdentityOwnershipError(old, auth.Catalog.MustRelation(a))
				}
				options.Owned, err = ownedSequenceLocked(e, scope, &auth, changes, &d, option.Owned)
				if err != nil {
					return core.Result{}, err
				}
			}
		}
		if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return core.Result{}, err
		}
		switch s.Action {
		case "rename":
			d.ObjectName = s.Target.Text
		case "logged", "unlogged":
			if old.Sequence.Persistence == "temporary" {
				return core.Result{}, sequence.TemporaryLoggedError(old, auth.Catalog.MustNamespace(old.Namespace()).Name())
			}
			options.Persistence = s.Action
		case "owner":
			target := ownerTarget
			if options.Owned != (ids.ObjectAddress{}) {
				a := options.Owned
				a.SubID = 0
				return core.Result{}, sequence.LinkedError(&d, auth.Catalog.MustRelation(a), false)
			}
			actor, _ := auth.Roles.Descriptor(auth.State.CurrentUser)
			if !actor.Superuser {
				role, _ := auth.Roles.Descriptor(target)
				allowed, err := auth.Roles.CanSetRole(actor.Ref, target)
				if err != nil {
					return core.Result{}, err
				}
				if !allowed {
					return core.Result{}, core.SQLError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
				}
				targetAuth := auth
				targetAuth.State.CurrentUser = target
				if err = targetAuth.CheckCreate(e.Catalog.Domain.Ref(ref.Epoch, d.Namespace())); err != nil {
					return core.Result{}, err
				}
			}
			d.Owner = target
			d.Security.ACL = authorization.SchemaACLOwner(d.Security.ACL, old.Owner, d.Owner)
		case "schema":
			if options.Owned != (ids.ObjectAddress{}) {
				a := options.Owned
				a.SubID = 0
				return core.Result{}, sequence.LinkedError(&d, auth.Catalog.MustRelation(a), true)
			}
			destination := &parser.CreateSequence{Name: parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}}, Persistence: "logged"}
			ns, err := e.RelationCreationNamespaceLocked(auth, destination.Name, destination.Persistence, changes)
			if err != nil {
				return core.Result{}, err
			}
			// pg_temp may stage a namespace. Resolve permissions and endpoints
			// against that same private catalog, without publishing on rejection.
			auth.Catalog = e.PrivateCatalogLocked(changes)
			auth.Recapture(auth.Catalog)
			if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
				auth.StagedTemporary = table.Address()
			}
			if err = auth.CheckCreate(ns); err != nil {
				return core.Result{}, err
			}
			if _, err = access.AdjustRelationCreationPersistence(auth.View, auth, ns, destination.Persistence); err != nil {
				return core.Result{}, err
			}
			if err = sequence.CheckSchemaMove(auth.Catalog.MustNamespace(d.Namespace()), auth.Catalog.MustNamespace(ns.Address)); err != nil {
				return core.Result{}, err
			}
			d.ObjectNamespace = ns.Address
		}
		effects := sequence.AlterEffects(old, &d, s.Options, s.Action)
		if effects.PlanStamp {
			d.SequencePlanStamp = rev
		}
		key := catalog.DescriptorNameKey(&d)
		if a, exists := auth.Catalog.LookupName(key); exists && a != d.Address() {
			return core.Result{}, core.SQLError("42P07", fmt.Sprintf(`relation "%s" already exists`, d.Name()))
		}
		e.CatalogChangeLocked(changes, d.Address(), &d)
		if effects.ReplaceGenerator {
			changes.Generators[d.Address()] = sequence.ReplacementGenerator(*state, rev, restart)
		}
		if effects.DiscardTail {
			if m := core.PayloadOf(op.Session).Sequences.Memory(d.Address()); m != nil {
				m.DiscardTailLocked()
			}
		}
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
		scope.Catalog = changes
		return core.Result{}, nil
	case *parser.DropSequence:
		return dropSequences(e, scope, s)
	}
	return core.Result{}, core.SQLError("0A000", "unsupported sequence statement")
}
func dropSequences(e *core.Engine, scope *core.StatementScope, s *parser.DropSequence) (core.Result, error) {
	refs := make([]ids.ObjectRef, 0, len(s.Names))
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	for _, name := range s.Names {
		ref, er := e.ResolveRelationReference(auth, name, false)
		if er != nil {
			if d, ok := er.(*sqlstate.Error); s.IfExists && ok && d.Code == "42P01" {
				scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`sequence "%s" does not exist, skipping`, catalog.ObjectNameText(name))})
				continue
			}
			e.Mu.Unlock()
			if d, ok := er.(*sqlstate.Error); ok && d.Code == "42P01" {
				return core.Result{}, core.SQLError("42P01", fmt.Sprintf(`sequence "%s" does not exist`, catalog.ObjectNameText(name)))
			}
			return core.Result{}, er
		}
		d := auth.Catalog.MustRelation(ref.Address)
		if d.RelationKind != catalog.SequenceRelation {
			e.Mu.Unlock()
			return core.Result{}, sequence.NotSequenceError(d)
		}
		if er = index.RelationDropOwner(auth.View, auth, ref, d); er != nil {
			e.Mu.Unlock()
			return core.Result{}, er
		}
		if d.Sequence.Identity {
			e.Mu.Unlock()
			return core.Result{}, sequence.IdentityDropError(auth.Catalog, d)
		}
		refs = append(refs, ref)
	}
	e.Mu.Unlock()
	for _, ref := range refs {
		if err = scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	auth, err = e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	changes := scope.PendingCatalog().Clone()
	targets := make([]ids.ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
	}
	// Every stored-expression owner shares one private dependency closure.
	checkStage := e.BeginCatalogStage(auth.Catalog)
	checkStage.AuthorizeLocked(auth, scope.Op.Session)
	defer e.DiscardCatalogStage(checkStage)
	checkStage.DependencyPath, err = core.ReferencePath(auth)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	e.Mu.Unlock()
	if err := scope.StageExpressionDependents(checkStage, targets, s.Mode, changes); err != nil {
		return core.Result{}, err
	}
	if _, err := e.ValidateCatalogStage(scope.Op.Context(), checkStage); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if auth.Catalog.Epoch() != e.Catalog.State.Epoch() {
		return core.Result{}, ids.ErrCatalogReplaced
	}
	if err := changes.MergeCandidate(e.Catalog.State, auth.Catalog.State(), &checkStage.Candidate); err != nil {
		return core.Result{}, err
	}
	cascade := checkStage.DropCascadeNames
	for _, ref := range refs {
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return core.Result{}, ids.ErrCatalogReplaced
		}
		if err = index.RelationDropOwner(auth.View, auth, ref, d); err != nil {
			return core.Result{}, err
		}
		if d.Sequence.Identity {
			return core.Result{}, sequence.IdentityDropError(auth.Catalog, d)
		}
		e.CatalogChangeLocked(changes, ref.Address, nil)
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
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	scope.Catalog = changes
	view.EmitCascade(scope.Op, cascade)
	return core.Result{}, nil
}
