package engine

import (
	"fmt"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)

func (e *Engine) executeExtensionUpdate(scope *StatementScope, s *parser.AlterExtensionUpdate, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	stage, err := e.stageExtensionUpdateLocked(scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	return e.publishExtensionStage(scope, stage)
}

func (e *Engine) stageExtensionUpdateLocked(scope *StatementScope, s *parser.AlterExtensionUpdate) (*CatalogStage, error) {
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
