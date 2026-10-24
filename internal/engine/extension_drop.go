package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeDropExtension(scope *StatementScope, s *parser.DropExtension, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	selected, err := extension.DropTargets(auth, auth.View, s, func(n sqlstate.Error) { scope.op.EmitNotice(session.LevelNotice, n) })
	if err != nil {
		return fail(err)
	}
	roots := extension.RemovalRoots(auth.Catalog, selected)
	stage := e.ownershipStage(auth, scope.op)
	stage.dependencyPath, _ = referencePath(auth)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, address := range access.OwnershipRelationLocks(stage.Base, roots, nil) {
		if err := scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), address), lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	for _, address := range roots {
		if err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), address), false); err != nil {
			break
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	subject := ids.ObjectAddress{}
	if len(selected) == 1 {
		subject = selected[0]
	}
	if err = stageExpressionDependencies(scope.op.Context(), stage, roots, s.Mode, scope, nil, nil, subject); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "2BP01" && len(selected) > 1 {
			diagnostic.Message = "cannot drop desired object(s) because other objects depend on them"
		}
		return Result{}, err
	}
	if err = scope.stageOwnedSequences(stage, roots, s.Mode, nil); err != nil {
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	result, err := e.publishOwnership(scope, stage)
	if err == nil && s.Mode == parser.DropCascade && len(stage.dropCascadeNames) > 0 {
		message, detail := stage.dropCascadeNames[0], ""
		if len(stage.dropCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.dropCascadeNames))
			detail = strings.Join(stage.dropCascadeNames, "\n")
		}
		scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
