package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Called with e.mu held; every return releases it. Preflight the whole group
// and its stable-column AUTO sequence dependencies before publication.
func (e *Engine) dropTableLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.DropTable, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if len(s.Names) == 0 || s.Mode != parser.DropRestrict && s.Mode != parser.DropCascade {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: invalid table drop")
	}
	refs, notices, err := DropTargets(ddlHost{e: e, op: op}, auth, s)
	if err != nil {
		e.mu.Unlock()
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
	if !temporary && scope.owner.State == session.TxActive {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	if len(refs) == 0 {
		e.mu.Unlock()
		for _, notice := range notices {
			op.EmitNotice(session.LevelNotice, notice)
		}
		return Result{}, nil
	}
	base := auth.Catalog
	var changes *session.Journal
	if temporary {
		changes = scope.owner.Catalog.Clone()
	}
	stage := e.BeginCatalogStage(base)
	stage.authorizeLocked(auth, op.Session)
	path, err := e.referencePath(auth)
	if err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	stage.dependencyPath = path
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, ref := range refs {
		if err := scope.acquirePartitionDropLocks(ref); err != nil {
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
	if err := scope.stageExpressionDependents(stage, targets, s.Mode, changes); err != nil {
		return Result{}, err
	}
	if err := scope.stageOwnedSequences(stage, targets, s.Mode, changes); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if temporary {
		if err := e.stageCatalogOverlay(scope, stage, changes); err != nil {
			return Result{}, err
		}
		scope.owner.AccessedTemporary = true
		emitViewCascade(op, stage.dropCascadeNames)
		for _, notice := range notices {
			op.EmitNotice(session.LevelNotice, notice)
		}
		return Result{}, nil
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	emitViewCascade(op, append(stage.schemaCascadeNames, stage.dropCascadeNames...))
	for _, notice := range notices {
		op.EmitNotice(session.LevelNotice, notice)
	}
	return Result{}, nil
}
