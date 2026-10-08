package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Utility admission pins the target identity before waiting. Shared rename and
// namespace operations retain their ordinary descriptor publication path.
func (e *Engine) executeAlterView(scope *StatementScope, s *parser.AlterView, count int) (Result, error) {
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if err := scope.validateLockedRelation(e); err != nil {
		return fail(err)
	}
	auth, err := e.statementAuthorizationLocked(scope.op, s)
	if err != nil {
		return fail(err)
	}
	ref, err := e.resolveRelationReference(auth, s.Name, true)
	if s.IfExists && renameMissing(err) {
		scope.op.session.mu.Lock()
		err = publicationOperationLocked(e, scope.op)
		scope.op.session.mu.Unlock()
		if err == nil {
			err = scope.op.Context().Err()
		}
		if err != nil {
			return fail(err)
		}
		scope.completed = true
		e.mu.Unlock()
		scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, s.Name.Parts[len(s.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ErrCatalogReplaced)
	}
	old := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if old.relationKind != viewRelation {
		return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a view`, old.name)))
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	switch s.Action {
	case "rename", "column":
		return e.renameRelationLocked(scope, scope.op, auth, s.Name, s.Column.Text, s.Target.Text, s.IfExists, s.Action == "column", count)
	case "schema":
		return e.alterSchemaLocked(scope, scope.op, auth, &parser.AlterTableSetSchema{Name: s.Name, Schema: s.Target, IfExists: s.IfExists, Pos: s.Pos}, count)
	}
	next := *old
	next.revision, err = e.revisionLocked()
	if err != nil {
		return fail(err)
	}
	switch s.Action {
	case "owner":
		target, err := schemaOwner(auth, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return fail(err)
		}
		actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
		if err != nil {
			return fail(err)
		}
		if !actor.Superuser && target != old.owner {
			allowed, err := auth.roles.canSetRole(actor.Ref, target)
			if err != nil {
				return fail(err)
			}
			if !allowed {
				role, _ := auth.roles.Descriptor(target)
				return fail(sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name)))
			}
			targetAuth := auth
			targetAuth.state.CurrentUser = target
			if err := targetAuth.CheckCreate(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: old.namespace}); err != nil {
				return fail(err)
			}
		}
		next.owner = target
		next.acl = schemaACLOwner(old.acl, old.owner, target)
		if old.columnACL != nil {
			next.columnACL = make(map[int32]ACLValue, len(old.columnACL))
			for attribute, acl := range old.columnACL {
				next.columnACL[attribute] = schemaACLOwner(acl, old.owner, target)
			}
		}
	case "set", "reset":
		definition := *old.view
		if s.Action == "set" {
			barrier, invoker, err := viewSecurity(s.Options)
			if err != nil {
				return fail(err)
			}
			for _, option := range s.Options {
				if option.Name.Text == "security_barrier" {
					definition.barrier = barrier
				} else {
					definition.invoker = invoker
				}
			}
		} else {
			for _, option := range s.Options {
				if option.HasValue {
					return fail(sqlError("42601", "RESET must not include values for parameters"))
				}
				switch option.Name.Text {
				case "security_barrier":
					definition.barrier = false
				case "security_invoker":
					definition.invoker = false
				}
			}
		}
		next.view = &definition
	default:
		return fail(sqlError("0A000", "ALTER VIEW action is unsupported"))
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, auth.revision, auth.roles.state.revision
	e.mu.Unlock()
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(scope.op, prepared, PublicationToken{Epoch: ref.Epoch})
		}
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func viewDropOwner(auth AuthorizationView, ref ObjectRef, d *relationDescriptor) error {
	ownerErr := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	if ownerErr == nil {
		return nil
	}
	ns := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor)
	allowed, err := auth.roles.inheritsRole(auth.state.CurrentUser, ns.owner)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	return ownerErr
}

// Resolve the complete group before locking; names are never replayed after a
// wait. The dependency closure and all removals publish as one catalog stage.
func (e *Engine) executeDropView(scope *StatementScope, s *parser.DropView, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(scope.op, s)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	refs := make([]ObjectRef, 0, len(s.Names))
	seen := make(map[ObjectAddress]bool, len(s.Names))
	var notices []sqlstate.Error
	for _, name := range s.Names {
		ref, err := e.resolveRelationReference(auth, name, true)
		if err != nil {
			if !s.IfExists || !renameMissing(err) {
				e.mu.Unlock()
				return Result{}, viewDropMissing(err, name)
			}
			diagnostic := viewDropMissing(err, name).(*sqlstate.Error)
			notices = append(notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: diagnostic.Message + ", skipping"})
			continue
		}
		d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if d.relationKind != viewRelation {
			hint := "Use DROP TABLE to remove a table."
			switch d.relationKind {
			case sequenceRelation:
				hint = "Use DROP SEQUENCE to remove a sequence."
			case indexRelation:
				hint = "Use DROP INDEX to remove an index."
			}
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a view`, d.name), Hint: hint}
		}
		if err := viewDropOwner(auth, ref, d); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if !seen[ref.Address] {
			seen[ref.Address] = true
			refs = append(refs, ref)
		}
	}
	epoch := auth.catalog.Epoch()
	e.mu.Unlock()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := relationLockPolicy{timeout: scope.op.lockTimeout, statementDeadline: scope.op.statementDeadline, source: scope.op.sourceContext}
	for _, ref := range refs {
		request, err := e.locks.startAcquire(scope.op.Context(), scope.ticket, lockResource{ObjectRef: ref}, accessExclusive, policy)
		if request != nil {
			if scope.replacementRead {
				e.replacementMu.RUnlock()
				scope.replacementRead = false
			}
			err = request.wait()
		}
		if err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	e.mu.Lock()
	auth, err = e.statementAuthorizationLocked(scope.op, s)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if epoch != auth.catalog.Epoch() {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	for _, ref := range refs {
		d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || ref.owner != e.owner {
			e.mu.Unlock()
			return Result{}, ErrCatalogReplaced
		}
		if err := viewDropOwner(auth, ref, d); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, auth.revision, auth.roles.state.revision
	stage.dependencyPath, err = e.referencePath(auth)
	if err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	targets := make([]ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
		if err := stage.Remove(ref); err != nil {
			return Result{}, err
		}
	}
	if err := stageViewDependents(stage, targets, s.Mode); err != nil {
		return Result{}, err
	}
	// Cascaded view identities receive the same AX admission as explicit targets.
	cascade := make([]ObjectRef, 0, len(stage.removals))
	for address, removal := range stage.removals {
		if !seen[address] {
			cascade = append(cascade, removal.ref)
		}
	}
	sort.Slice(cascade, func(i, j int) bool { return cascade[i].Address.ObjectID < cascade[j].Address.ObjectID })
	for _, ref := range cascade {
		request, err := e.locks.startAcquire(scope.op.Context(), scope.ticket, lockResource{ObjectRef: ref}, accessExclusive, policy)
		if request != nil {
			err = request.wait()
		}
		if err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, prepared, PublicationToken{Epoch: epoch})
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	emitViewCascade(scope.op, stage.viewCascadeNames)
	for _, notice := range notices {
		scope.op.emitNotice(levelNotice, notice)
	}
	return Result{}, nil
}

func viewDropMissing(err error, name parser.ObjectName) error {
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "42P01" {
		return err
	}
	return sqlError("42P01", fmt.Sprintf(`view "%s" does not exist`, name.Parts[len(name.Parts)-1].Text))
}
