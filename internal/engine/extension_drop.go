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

func executeDropExtension(e *Engine, scope *StatementScope, s *parser.DropExtension, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	selected, err := extension.DropTargets(auth, auth.View, s, func(n sqlstate.Error) { scope.Op.EmitNotice(session.LevelNotice, n) })
	if err != nil {
		return fail(err)
	}
	roots := extension.RemovalRoots(auth.Catalog, selected)
	stage := OwnershipStage(e, auth, scope.Op)
	stage.DependencyPath, _ = ReferencePath(auth)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, address := range access.OwnershipRelationLocks(stage.Base, roots, nil) {
		if err := scope.AcquireSequence(e.Catalog.Domain.Ref(stage.Base.Epoch(), address), lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	for _, address := range roots {
		if err = stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), address), false); err != nil {
			break
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	subject := ids.ObjectAddress{}
	if len(selected) == 1 {
		subject = selected[0]
	}
	if err = StageExpressionDependencies(scope.Op.Context(), stage, roots, s.Mode, scope, nil, nil, subject); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "2BP01" && len(selected) > 1 {
			diagnostic.Message = "cannot drop desired object(s) because other objects depend on them"
		}
		return Result{}, err
	}
	if err = scope.StageOwnedSequences(stage, roots, s.Mode, nil); err != nil {
		return Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	result, err := PublishOwnership(e, scope, stage)
	if err == nil && s.Mode == parser.DropCascade && len(stage.DropCascadeNames) > 0 {
		message, detail := stage.DropCascadeNames[0], ""
		if len(stage.DropCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.DropCascadeNames))
			detail = strings.Join(stage.DropCascadeNames, "\n")
		}
		scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
