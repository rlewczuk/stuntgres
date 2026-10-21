package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
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
	if s.IfExists && table.RenameMissing(err) {
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
	if err = view.AlterViewAuthority(auth.View, auth, ref, s); err != nil {
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
	next, extension, err := view.AlterChange(ddlHost{e: e, op: scope.op}, auth.Catalog, old, s)
	if err != nil {
		return fail(err)
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
	if next != nil {
		err = stage.Replace(next)
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
	refs, notices, err := view.DropTargets(ddlHost{e: e, op: scope.op}, auth, s)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	seen := make(map[ids.ObjectAddress]bool, len(refs))
	for _, ref := range refs {
		seen[ref.Address] = true
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
	if err := view.RecheckDropTargets(auth.View, auth, refs); err != nil {
		e.mu.Unlock()
		return Result{}, err
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
