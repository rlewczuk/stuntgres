package engine

import (
	"fmt"

	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func executeForeignDDL(e *Engine, scope *StatementScope, s *parser.ForeignDDL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Kind == "table" && s.Action == "alter" {
		return executeForeignTableAlter(e, scope, s)
	}
	if s.Action == "import" {
		return executeForeignImport(e, scope, s)
	}
	op := scope.Op
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	jobs, roots, err := stageForeignLocked(e, scope, auth, stage, s)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if len(roots) != 0 {
		if err := scope.StageExpressionDependents(stage, roots, s.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err := fdw.RunValidations(op.Context(), jobs); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

// stageForeignLocked stages WRAPPER, SERVER, USER MAPPING and CREATE or
// DROP FOREIGN TABLE through fdw.StageDDL on the statement's ddlHost.
func stageForeignLocked(e *Engine, scope *StatementScope, auth AuthorizationView, stage *CatalogStage, s *parser.ForeignDDL) ([]fdw.Validation, []ids.ObjectAddress, error) {
	return fdw.StageDDL(TableHost{DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, s, scope.Input)
}
