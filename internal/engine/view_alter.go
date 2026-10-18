package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PostgreSQL uses different callbacks for column, dependency and other forms.
// Keep admission and execution on the same action-specific authority contract.
func alterViewAuthority(auth AuthorizationView, ref ids.ObjectRef, s *parser.AlterView) error {
	d := auth.catalog.MustRelation(ref.Address)
	kind, label := viewRelation, "view"
	ownerChecked := false
	if s.Materialized {
		kind, label = materializedViewRelation, "materialized view"
		if s.Action == "column" {
			return auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		}
		if s.Action != "depends" && s.Action != "no_depends" {
			if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
				return err
			}
			ownerChecked = true
		}
	}
	if d.RelationKind != kind {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, d.name, label))
	}
	if ownerChecked {
		return nil
	}
	return auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
}

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
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.catalog.MustRelation(ref.Address)
	if err = alterViewAuthority(auth, ref, s); err != nil {
		return fail(err)
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	switch s.Action {
	case "rename", "column":
		return e.renameRelationLocked(scope, scope.op, auth, s.Name, s.Column.Text, s.Target.Text, s.IfExists, s.Action == "column", s.Materialized && s.Action == "column", false, count)
	case "schema":
		return e.alterSchemaLocked(scope, scope.op, auth, &parser.AlterTableSetSchema{Name: s.Name, Schema: s.Target, IfExists: s.IfExists, Pos: s.Pos}, count)
	}
	next := *old
	changed := true
	var extension *extensionDescriptor
	switch s.Action {
	case "depends", "no_depends":
		address, exists := auth.catalog.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Target.Text})
		if !exists {
			return fail(sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, s.Target.Text)))
		}
		extension, _ = auth.catalog.Extension(address)
		if extension == nil {
			return fail(ids.ErrCatalogReplaced)
		}
		slot := sort.Search(len(old.AutoExtensions), func(i int) bool { return old.AutoExtensions[i].ObjectID >= address.ObjectID })
		present := slot < len(old.AutoExtensions) && old.AutoExtensions[slot] == address
		changed = present == (s.Action == "no_depends")
		if changed {
			if present {
				next.AutoExtensions = make([]ids.ObjectAddress, len(old.AutoExtensions)-1)
				copy(next.AutoExtensions, old.AutoExtensions[:slot])
				copy(next.AutoExtensions[slot:], old.AutoExtensions[slot+1:])
			} else {
				next.AutoExtensions = make([]ids.ObjectAddress, len(old.AutoExtensions)+1)
				copy(next.AutoExtensions, old.AutoExtensions[:slot])
				next.AutoExtensions[slot] = address
				copy(next.AutoExtensions[slot+1:], old.AutoExtensions[slot:])
			}
		}
	case "set", "reset":
		definition := *viewOf(old)
		if s.Action == "set" {
			barrier, invoker, mode, err := viewOptions(s.Options)
			if err != nil {
				return fail(err)
			}
			for _, option := range s.Options {
				switch option.Name.Text {
				case "security_barrier":
					definition.barrier = barrier
				case "security_invoker":
					definition.invoker = invoker
				case "check_option":
					definition.checkOption = mode
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
				case "check_option":
					definition.checkOption = viewCheckNone
				}
			}
		}
		definition.options = transformViewOptions(viewOf(old).options, s.Options, s.Action == "reset")
		if definition.checkOption != viewCheckNone {
			if err := validateViewCheckDeclaration(&definition); err != nil {
				return fail(err)
			}
		}
		next.View = &definition
	default:
		return fail(sqlError("0A000", "ALTER VIEW action is unsupported"))
	}
	if changed {
		next.revision, err = e.catalog.revisionLocked()
		if err != nil {
			return fail(err)
		}
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, scope.op.session)
	if extension != nil {
		stage.querySources = []Descriptor{extension}
	}
	e.mu.Unlock()
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	defer e.DiscardCatalogStage(stage)
	if changed {
		err = stage.Replace(&next)
	}
	if err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
		}
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func viewDropOwner(auth AuthorizationView, ref ids.ObjectRef, d *relationDescriptor) error {
	ownerErr := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	if ownerErr == nil {
		return nil
	}
	ns := auth.catalog.MustNamespace(d.namespace)
	allowed, err := auth.roles.InheritsRole(auth.state.CurrentUser, ns.Owner)
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
	kind, label := viewRelation, "view"
	if s.Materialized {
		kind, label = materializedViewRelation, "materialized view"
	}
	refs := make([]ids.ObjectRef, 0, len(s.Names))
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	var notices []sqlstate.Error
	for _, name := range s.Names {
		ref, err := e.resolveRelationReference(auth, name, true)
		if err != nil {
			if !s.IfExists || !renameMissing(err) {
				e.mu.Unlock()
				return Result{}, viewDropMissing(err, name, label)
			}
			diagnostic := viewDropMissing(err, name, label).(*sqlstate.Error)
			notices = append(notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: diagnostic.Message + ", skipping"})
			continue
		}
		d := auth.catalog.MustRelation(ref.Address)
		if d.RelationKind != kind {
			hint := "Use DROP TABLE to remove a table."
			switch d.RelationKind {
			case sequenceRelation:
				hint = "Use DROP SEQUENCE to remove a sequence."
			case indexRelation:
				hint = "Use DROP INDEX to remove an index."
			case viewRelation:
				hint = "Use DROP VIEW to remove a view."
			case materializedViewRelation:
				hint = "Use DROP MATERIALIZED VIEW to remove a materialized view."
			}
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a %s`, d.name, label), Hint: hint}
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
	policy := lock.Policy{TimeoutSlot: &scope.op.lockWait, Timeout: scope.op.lockTimeout, StatementDeadline: scope.op.statementDeadline, Source: scope.op.sourceContext}
	for _, ref := range refs {
		request, err := e.locks.StartAcquire(scope.op.Context(), scope.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		if request != nil {
			if scope.replacementRead {
				e.replacementMu.RUnlock()
				scope.replacementRead = false
			}
			err = request.Wait()
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
		return Result{}, ids.ErrCatalogReplaced
	}
	for _, ref := range refs {
		d, ok := auth.catalog.Relation(ref.Address)
		if !ok || ref.Domain() != e.catalog.domain {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		if err := viewDropOwner(auth, ref, d); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.dependencyPath, err = e.referencePath(auth)
	if err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	targets := make([]ids.ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
		if err := stage.Remove(ref); err != nil {
			return Result{}, err
		}
	}
	if err := scope.stageExpressionDependents(stage, targets, s.Mode, nil); err != nil {
		return Result{}, err
	}
	// Cascaded view identities receive the same AX admission as explicit targets.
	cascade := make([]ids.ObjectRef, 0, len(stage.removals))
	for address, removal := range stage.removals {
		if address.ClassID != ids.RelationClassID {
			continue
		}
		if !seen[address] {
			cascade = append(cascade, removal.ref)
		}
	}
	sort.Slice(cascade, func(i, j int) bool { return cascade[i].Address.ObjectID < cascade[j].Address.ObjectID })
	for _, ref := range cascade {
		request, err := e.locks.StartAcquire(scope.op.Context(), scope.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		if request != nil {
			err = request.Wait()
		}
		if err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: epoch})
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	emitViewCascade(scope.op, append(stage.schemaCascadeNames, stage.viewCascadeNames...))
	for _, notice := range notices {
		scope.op.emitNotice(levelNotice, notice)
	}
	return Result{}, nil
}

func viewDropMissing(err error, name parser.ObjectName, label string) error {
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "42P01" {
		return err
	}
	return sqlError("42P01", fmt.Sprintf(`%s "%s" does not exist`, label, name.Parts[len(name.Parts)-1].Text))
}
