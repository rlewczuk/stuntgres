package engine

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// Called with e.mu held; every return releases it. Preflight the whole group
// and its stable-column AUTO sequence dependencies before publication.
func dropTableLocked(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.DropTable, count int) (Result, error) {
	if count != 0 {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if len(s.Names) == 0 || s.Mode != parser.DropRestrict && s.Mode != parser.DropCascade {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: invalid table drop")
	}
	refs, notices, err := table.DropTargets(TableHost{DDLHost{E: e, Op: op}}, auth, s)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	temporary := true
	for _, ref := range refs {
		relation := auth.Catalog.Get(ref.Address)
		namespace := auth.Catalog.MustNamespace(relation.Namespace())
		if namespace.Temporary != op.Session.SessionToken {
			temporary = false
			break
		}
	}
	if len(refs) == 0 {
		e.Mu.Unlock()
		for _, notice := range notices {
			op.EmitNotice(session.LevelNotice, notice)
		}
		return Result{}, nil
	}
	// Ordinary roots need AX too; the partition closure helper below cannot
	// supply this hold for nonpartitioned tables. Readmit the captured group
	// after all waits, before constructing its one private candidate.
	e.Mu.Unlock()
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	auth, err = e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	for _, ref := range refs {
		if auth.Catalog.Get(ref.Address) == nil {
			e.Mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
	}
	fresh, freshNotices, err := table.DropTargets(TableHost{DDLHost{E: e, Op: op}}, auth, s)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if len(fresh) != len(refs) {
		e.Mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	slices.SortFunc(fresh, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for i := range refs {
		if fresh[i] != refs[i] {
			e.Mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
	}
	notices = freshNotices
	base := auth.Catalog
	var changes *session.Journal
	if temporary {
		changes = scope.Owner.Catalog.Clone()
	}
	stage := e.BeginCatalogStage(base)
	stage.AuthorizeLocked(auth, op.Session)
	path, err := ReferencePath(auth)
	if err != nil {
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	stage.DependencyPath = path
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, ref := range refs {
		if err := scope.AcquirePartitionDropLocks(ref); err != nil {
			return Result{}, err
		}
		if err := stage.Remove(ref); err != nil {
			return Result{}, err
		}
	}
	targets := make([]ids.ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
	}
	if err := scope.StageExpressionDependents(stage, targets, s.Mode, changes); err != nil {
		return Result{}, err
	}
	if err := scope.StageOwnedSequences(stage, targets, s.Mode, changes); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if temporary {
		if err := e.StageCatalogOverlay(scope, stage, changes); err != nil {
			return Result{}, err
		}
		scope.Owner.AccessedTemporary = true
		view.EmitCascade(op, stage.DropCascadeNames)
		for _, notice := range notices {
			op.EmitNotice(session.LevelNotice, notice)
		}
		return Result{}, nil
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.Completed = true
	view.EmitCascade(op, append(stage.SchemaCascadeNames, stage.DropCascadeNames...))
	for _, notice := range notices {
		op.EmitNotice(session.LevelNotice, notice)
	}
	return Result{}, nil
}
