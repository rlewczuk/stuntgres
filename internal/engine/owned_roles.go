package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeOwned(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	plan, err := role.PlanOwned(auth.View, stmt, func(address ids.ObjectAddress) string {
		path, _ := auth.ReferencePath()
		return stored.DependencyObject(auth.Catalog, address, path)
	})
	if err != nil {
		return fail(err)
	}
	if plan.Noop {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	reassign, sources, mode, roots, primaryRoot := plan.Reassign, plan.Sources, plan.Mode, plan.Roots, plan.PrimaryRoot
	stage := e.ownershipStage(auth, scope.op)
	stage.dependencyPath, _ = referencePath(auth)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	// Pin roots and automatic associations before producing private candidates.
	for _, a := range access.OwnershipRelationLocks(stage.Base, roots) {
		if err = scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		ref := e.catalog.Domain.Ref(stage.Base.Epoch(), a)
		if err = scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	err = role.StageOwned(ddlHost{e: e, op: scope.op, stage: stage}, auth, plan)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if !reassign {
		// PostgreSQL collects actual policy and grantor-membership deletions
		// alongside ownership roots. Prepare cleanup once, still entirely private,
		// before discovering consumers of the complete pending deletion union.
		if err = role.StageOwnedCleanup(scope.op.Context(), stage, auth.Roles, auth.RoleRecord, sources); err != nil {
			return Result{}, err
		}
		if primaryRoot != (ids.ObjectAddress{}) && role.OwnedCleanupAddsDeletionRoot(stage, sources) {
			primaryRoot = ids.ObjectAddress{}
		}
		if err = scope.stageExpressionDependents(stage, roots, mode, nil); err != nil {
			primary := ""
			if primaryRoot != (ids.ObjectAddress{}) {
				primary = stored.DependencyObject(stage.Base, primaryRoot, stage.dependencyPath)
			}
			return Result{}, role.OwnedDependentsError(err, primary)
		}
		if err = scope.stageOwnedSequences(stage, roots, mode, nil); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	result, err := e.publishOwnership(scope, stage)
	if err == nil && !reassign && mode == parser.DropCascade && len(stage.dropCascadeNames) > 0 {
		message, detail := stage.dropCascadeNames[0], ""
		if len(stage.dropCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.dropCascadeNames))
			detail = strings.Join(stage.dropCascadeNames, "\n")
		}
		scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
