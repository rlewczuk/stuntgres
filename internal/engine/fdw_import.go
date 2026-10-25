package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func executeForeignImport(e *core.Engine, scope *core.StatementScope, s *parser.ForeignDDL) (core.Result, error) {
	if err := core.CheckRestoreEffect(scope.Op); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	importer, request, err := fdw.PlanImport(tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, s)
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	definitions, err := importer.ImportForeignSchema(scope.Op.Context(), request)
	if err != nil {
		return core.Result{}, err
	}
	var jobs []fdw.Validation
	for _, definition := range definitions {
		if err := scope.Op.Context().Err(); err != nil {
			return core.Result{}, err
		}
		if !fdw.ImportSelected(s, definition.Name) {
			continue
		}
		table, err := fdw.ImportedTable(scope.Op.Context(), definition, s)
		if err != nil {
			return core.Result{}, fdw.ImportError(err, definition.Name)
		}
		e.Mu.Lock()
		_, currentErr := e.AuthorizationLocked(scope.Op)
		if currentErr == nil && e.Catalog.State.Epoch() != stage.Base.Epoch() {
			currentErr = ids.ErrCatalogReplaced
		}
		var validations []fdw.Validation
		if currentErr == nil {
			validations, _, currentErr = fdw.StageTable(tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, table, scope.Input)
		}
		e.Mu.Unlock()
		if currentErr != nil {
			return core.Result{}, fdw.ImportError(currentErr, definition.Name)
		}
		for i := range validations {
			validations[i].ImportTable = definition.Name
		}
		jobs = append(jobs, validations...)
	}
	if err = fdw.RunValidations(scope.Op.Context(), jobs); err != nil {
		return core.Result{}, err
	}
	if err = scope.AcquireDefinitionProviders(stage); err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return core.Result{}, err
}
