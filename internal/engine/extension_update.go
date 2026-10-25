package engine

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)

func executeExtensionUpdate(e *core.Engine, scope *core.StatementScope, s *parser.AlterExtensionUpdate, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	stage, err := stageExtensionUpdateLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return core.Result{}, err
	}
	return publishExtensionStage(e, scope, stage)
}

func stageExtensionUpdateLocked(e *core.Engine, scope *core.StatementScope, s *parser.AlterExtensionUpdate) (*core.CatalogStage, error) {
	if err := scope.Op.Context().Err(); err != nil {
		return nil, err
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	staged, err := extension.StageUpdate(&extensionHost{engine: e, op: scope.Op, auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	return stageOf(staged)
}
