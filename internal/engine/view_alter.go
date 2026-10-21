package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PostgreSQL uses different callbacks for column, dependency and other forms.
// Keep admission and execution on the same action-specific authority contract.
func alterViewAuthority(view access.View, env access.Environment, ref ids.ObjectRef, s *parser.AlterView) error {
	d := view.Catalog.MustRelation(ref.Address)
	kind, label := catalog.ViewRelation, "view"
	ownerChecked := false
	if s.Materialized {
		kind, label = catalog.MaterializedViewRelation, "materialized view"
		if s.Action == "column" {
			return view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
		}
		if s.Action != "depends" && s.Action != "no_depends" {
			if err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
				return err
			}
			ownerChecked = true
		}
	}
	if d.RelationKind != kind {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, d.Name(), label))
	}
	if ownerChecked {
		return nil
	}
	return view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
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
		scope.op.Session.Mu.Lock()
		err = publicationOperationLocked(e, scope.op)
		scope.op.Session.Mu.Unlock()
		if err == nil {
			err = scope.op.Context().Err()
		}
		if err != nil {
			return fail(err)
		}
		scope.completed = true
		e.mu.Unlock()
		scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, s.Name.Parts[len(s.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if err = alterViewAuthority(auth.View, auth, ref, s); err != nil {
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
	var extension *catalog.Extension
	switch s.Action {
	case "depends", "no_depends":
		address, exists := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Target.Text})
		if !exists {
			return fail(sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, s.Target.Text)))
		}
		extension, _ = auth.Catalog.Extension(address)
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
		definition := ViewOf(old).Parts()
		if s.Action == "set" {
			barrier, invoker, mode, err := ViewOptions(s.Options)
			if err != nil {
				return fail(err)
			}
			for _, option := range s.Options {
				switch option.Name.Text {
				case "security_barrier":
					definition.Barrier = barrier
				case "security_invoker":
					definition.Invoker = invoker
				case "check_option":
					definition.CheckOption = mode
				}
			}
		} else {
			for _, option := range s.Options {
				if option.HasValue {
					return fail(sqlError("42601", "RESET must not include values for parameters"))
				}
				switch option.Name.Text {
				case "security_barrier":
					definition.Barrier = false
				case "security_invoker":
					definition.Invoker = false
				case "check_option":
					definition.CheckOption = CheckNone
				}
			}
		}
		definition.Options = transformViewOptions(ViewOf(old).Options(), s.Options, s.Action == "reset")
		altered := NewView(definition)
		if altered.CheckOption() != CheckNone {
			if err := ValidateViewCheckDeclaration(altered); err != nil {
				return fail(err)
			}
		}
		next.View = altered
	default:
		return fail(sqlError("0A000", "ALTER VIEW action is unsupported"))
	}
	if changed {
		next.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return fail(err)
		}
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	if extension != nil {
		stage.querySources = []catalog.Descriptor{extension}
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

func viewDropOwner(view access.View, env access.Environment, ref ids.ObjectRef, d *catalog.Relation) error {
	ownerErr := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
	if ownerErr == nil {
		return nil
	}
	ns := view.Catalog.MustNamespace(d.Namespace())
	allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, ns.Owner)
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
	kind, label := catalog.ViewRelation, "view"
	if s.Materialized {
		kind, label = catalog.MaterializedViewRelation, "materialized view"
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
		d := auth.Catalog.MustRelation(ref.Address)
		if d.RelationKind != kind {
			hint := "Use DROP TABLE to remove a table."
			switch d.RelationKind {
			case catalog.SequenceRelation:
				hint = "Use DROP SEQUENCE to remove a sequence."
			case catalog.IndexRelation:
				hint = "Use DROP INDEX to remove an index."
			case catalog.ViewRelation:
				hint = "Use DROP VIEW to remove a view."
			case catalog.MaterializedViewRelation:
				hint = "Use DROP MATERIALIZED VIEW to remove a materialized view."
			}
			e.mu.Unlock()
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a %s`, d.Name(), label), Hint: hint}
		}
		if err := viewDropOwner(auth.View, auth, ref, d); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if !seen[ref.Address] {
			seen[ref.Address] = true
			refs = append(refs, ref)
		}
	}
	epoch := auth.Catalog.Epoch()
	e.mu.Unlock()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := lock.Policy{TimeoutSlot: &scope.op.LockWait, Timeout: scope.op.LockTimeout, StatementDeadline: scope.op.StatementDeadline, Source: scope.op.SourceContext}
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
	if epoch != auth.Catalog.Epoch() {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	for _, ref := range refs {
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok || ref.Domain() != e.catalog.Domain {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		if err := viewDropOwner(auth.View, auth, ref, d); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
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
	cascade := make([]ids.ObjectRef, 0, stage.RemovalCount())
	for address, removal := range stage.Removals() {
		if address.ClassID != ids.RelationClassID {
			continue
		}
		if !seen[address] {
			cascade = append(cascade, removal.Ref)
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
	emitViewCascade(scope.op, append(stage.schemaCascadeNames, stage.dropCascadeNames...))
	for _, notice := range notices {
		scope.op.EmitNotice(session.LevelNotice, notice)
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
