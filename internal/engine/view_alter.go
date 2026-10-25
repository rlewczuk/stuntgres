package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// Utility admission pins the target identity before waiting. Shared rename and
// namespace operations retain their ordinary descriptor publication path.
func executeAlterView(e *core.Engine, scope *core.StatementScope, s *parser.AlterView, count int) (core.Result, error) {
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if err := scope.ValidateLockedRelation(e); err != nil {
		return fail(err)
	}
	auth, err := e.StatementAuthorizationLocked(scope.Op, s)
	if err != nil {
		return fail(err)
	}
	ref, err := e.ResolveRelationReference(auth, s.Name, true)
	if s.IfExists && table.RenameMissing(err) {
		return skipMissingRelationLocked(e, scope, scope.Op, s.Name.Parts[len(s.Name.Parts)-1].Text, false)
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.LockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if err = view.AlterViewAuthority(auth.View, auth, ref, s); err != nil {
		return fail(err)
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	switch s.Action {
	case "rename", "column":
		return renameRelationLocked(e, scope, scope.Op, auth, s.Name, s.Column.Text, s.Target.Text, s.IfExists, s.Action == "column", s.Materialized && s.Action == "column", false, count)
	case "schema":
		return alterSchemaLocked(e, scope, scope.Op, auth, &parser.AlterTableSetSchema{Name: s.Name, Schema: s.Target, IfExists: s.IfExists, Pos: s.Pos}, count)
	}
	next, extension, err := view.AlterChange(core.DDLHost{E: e, Op: scope.Op}, auth.Catalog, old, s)
	if err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	if extension != nil {
		stage.QuerySources = []catalog.Descriptor{extension}
	}
	e.Mu.Unlock()
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	defer e.DiscardCatalogStage(stage)
	if next != nil {
		err = stage.Replace(next)
	}
	if err == nil {
		var prepared *core.PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.Op.Context(), stage)
		if err == nil {
			_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: ref.Epoch})
		}
	}
	if err == nil {
		scope.Completed = true
	}
	return core.Result{}, err
}

// Resolve the complete group before locking; names are never replayed after a
// wait. The dependency closure and all removals publish as one catalog stage.
func executeDropView(e *core.Engine, scope *core.StatementScope, s *parser.DropView, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	auth, err := e.StatementAuthorizationLocked(scope.Op, s)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	refs, notices, err := view.DropTargets(ViewHost{TableHost{core.DDLHost{E: e, Op: scope.Op}}}, auth, s)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	seen := make(map[ids.ObjectAddress]bool, len(refs))
	for _, ref := range refs {
		seen[ref.Address] = true
	}
	epoch := auth.Catalog.Epoch()
	e.Mu.Unlock()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Address.ObjectID < refs[j].Address.ObjectID })
	policy := lock.Policy{TimeoutSlot: &scope.Op.LockWait, Timeout: scope.Op.LockTimeout, StatementDeadline: scope.Op.StatementDeadline, Source: scope.Op.SourceContext}
	for _, ref := range refs {
		request, err := e.Locks.StartAcquire(scope.Op.Context(), scope.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		if request != nil {
			if scope.ReplacementRead {
				e.ReplacementMu.RUnlock()
				scope.ReplacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return core.Result{}, err
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	e.Mu.Lock()
	auth, err = e.StatementAuthorizationLocked(scope.Op, s)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	if epoch != auth.Catalog.Epoch() {
		e.Mu.Unlock()
		return core.Result{}, ids.ErrCatalogReplaced
	}
	if err := view.RecheckDropTargets(auth.View, auth, refs); err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	stage.DependencyPath, err = core.ReferencePath(auth)
	if err != nil {
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	targets := make([]ids.ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
		if err := stage.Remove(ref); err != nil {
			return core.Result{}, err
		}
	}
	if err := scope.StageExpressionDependents(stage, targets, s.Mode, nil); err != nil {
		return core.Result{}, err
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
		request, err := e.Locks.StartAcquire(scope.Op.Context(), scope.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		if request != nil {
			err = request.Wait()
		}
		if err != nil {
			return core.Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: epoch})
	}
	if err != nil {
		return core.Result{}, err
	}
	scope.Completed = true
	view.EmitCascade(scope.Op, append(stage.SchemaCascadeNames, stage.DropCascadeNames...))
	for _, notice := range notices {
		scope.Op.EmitNotice(session.LevelNotice, notice)
	}
	return core.Result{}, nil
}
