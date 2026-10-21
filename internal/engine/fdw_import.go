package engine

import (
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) executeForeignImport(scope *StatementScope, s *parser.ForeignDDL) (Result, error) {
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, scope.op.Session)
	importer, request, err := fdw.PlanImport(ddlHost{e: e, op: scope.op, stage: stage}, auth, s)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	definitions, err := importer.ImportForeignSchema(scope.op.Context(), request)
	if err != nil {
		return Result{}, err
	}
	var jobs []fdw.Validation
	for _, definition := range definitions {
		if err := scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		if !fdw.ImportSelected(s, definition.Name) {
			continue
		}
		table, err := fdw.ImportedTable(scope.op.Context(), definition, s)
		if err != nil {
			return Result{}, fdw.ImportError(err, definition.Name)
		}
		e.mu.Lock()
		_, currentErr := e.authorizationLocked(scope.op)
		if currentErr == nil && e.catalog.state.Epoch() != stage.Base.Epoch() {
			currentErr = ids.ErrCatalogReplaced
		}
		var validations []fdw.Validation
		if currentErr == nil {
			validations, _, currentErr = fdw.StageTable(ddlHost{e: e, op: scope.op, stage: stage}, auth, table, scope.input)
		}
		e.mu.Unlock()
		if currentErr != nil {
			return Result{}, fdw.ImportError(currentErr, definition.Name)
		}
		for i := range validations {
			validations[i].ImportTable = definition.Name
		}
		jobs = append(jobs, validations...)
	}
	if err = fdw.RunValidations(scope.op.Context(), jobs); err != nil {
		return Result{}, err
	}
	if err = scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}
