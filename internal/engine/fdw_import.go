package engine

import (
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) executeForeignImport(scope *StatementScope, s *parser.ForeignDDL) (Result, error) {
	if err := CheckRestoreEffect(scope.Op); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	importer, request, err := fdw.PlanImport(DDLHost{E: e, Op: scope.Op, CatalogStage: stage}, auth, s)
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	definitions, err := importer.ImportForeignSchema(scope.Op.Context(), request)
	if err != nil {
		return Result{}, err
	}
	var jobs []fdw.Validation
	for _, definition := range definitions {
		if err := scope.Op.Context().Err(); err != nil {
			return Result{}, err
		}
		if !fdw.ImportSelected(s, definition.Name) {
			continue
		}
		table, err := fdw.ImportedTable(scope.Op.Context(), definition, s)
		if err != nil {
			return Result{}, fdw.ImportError(err, definition.Name)
		}
		e.Mu.Lock()
		_, currentErr := e.AuthorizationLocked(scope.Op)
		if currentErr == nil && e.Catalog.State.Epoch() != stage.Base.Epoch() {
			currentErr = ids.ErrCatalogReplaced
		}
		var validations []fdw.Validation
		if currentErr == nil {
			validations, _, currentErr = fdw.StageTable(DDLHost{E: e, Op: scope.Op, CatalogStage: stage}, auth, table, scope.Input)
		}
		e.Mu.Unlock()
		if currentErr != nil {
			return Result{}, fdw.ImportError(currentErr, definition.Name)
		}
		for i := range validations {
			validations[i].ImportTable = definition.Name
		}
		jobs = append(jobs, validations...)
	}
	if err = fdw.RunValidations(scope.Op.Context(), jobs); err != nil {
		return Result{}, err
	}
	if err = scope.AcquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}
