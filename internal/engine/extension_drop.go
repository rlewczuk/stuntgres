package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
	var selected []ids.ObjectAddress
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name.Text})
		if address == (ids.ObjectAddress{}) {
			if !s.IfExists {
				return fail(sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, name.Text)))
			}
			scope.op.EmitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`extension "%s" does not exist, skipping`, name.Text)})
			continue
		}
		if seen[address] {
			continue
		}
		if err := auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), address), OwnerOnly: true, OwnerNoun: "extension"}); err != nil {
			return fail(err)
		}
		selected = append(selected, address)
		seen[address] = true
	}
	roots := extension.RemovalRoots(auth.Catalog, selected)
	stage := e.ownershipStage(auth, scope.op)
	stage.dependencyPath, _ = e.referencePath(auth)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, address := range ownershipRelationLocks(stage.Base, roots) {
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
	if err == nil && s.Mode == parser.DropCascade && len(stage.viewCascadeNames) > 0 {
		message, detail := stage.viewCascadeNames[0], ""
		if len(stage.viewCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.viewCascadeNames))
			detail = strings.Join(stage.viewCascadeNames, "\n")
		}
		scope.op.EmitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
