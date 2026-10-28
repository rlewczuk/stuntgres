package schemacmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

// ExecuteForeignDDL runs the foreign-data DDL: FOREIGN DATA WRAPPER, SERVER
// and USER MAPPING statements, CREATE, ALTER and DROP FOREIGN TABLE, and
// IMPORT FOREIGN SCHEMA. Wrapper validations run with Engine.Mu released,
// before one publication.
func ExecuteForeignDDL(e *core.Engine, scope *core.StatementScope, s *parser.ForeignDDL, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
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
		return core.Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	jobs, roots, err := stageForeignLocked(e, scope, auth, stage, s)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return core.Result{}, err
	}
	if len(roots) != 0 {
		for _, address := range roots {
			stage.MarkDrop(address, catalog.DropProvenance{Original: true})
		}
		if err := scope.StageExpressionDependents(stage, roots, s.Mode, nil); err != nil {
			return core.Result{}, err
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if s.Kind == "table" && s.Action == "create" {
		return core.Result{}, finishForeignTableCreation(e, scope, stage, jobs)
	}
	if err := fdw.RunValidations(op.Context(), scope, jobs); err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return core.Result{}, err
}

// stageForeignLocked stages WRAPPER, SERVER, USER MAPPING and CREATE or
// DROP FOREIGN TABLE through fdw.StageDDL on the statement's ddlHost.
func stageForeignLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, stage *core.CatalogStage, s *parser.ForeignDDL) ([]fdw.Validation, []ids.ObjectAddress, error) {
	return fdw.StageDDL(tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, s, scope.Input)
}
