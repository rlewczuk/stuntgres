package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ExecuteExtensionUpdate runs ALTER EXTENSION UPDATE and publishes through
// the same path as ExecuteExtension.
func ExecuteExtensionUpdate(e *core.Engine, scope *core.StatementScope, s *parser.AlterExtensionUpdate, count int) (core.Result, error) {
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
	staged, err := extension.StageUpdate(&extensionHost{Engine: e, Op: scope.Op, Auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	stage, err := stageOf(staged)
	if err == nil {
		ddl.RecordCommand(stage, "ALTER EXTENSION", stage.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}))
	}
	return stage, err
}
