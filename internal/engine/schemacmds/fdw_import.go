package schemacmds

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
	importer, request, err := fdw.PlanImport(tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, s, auth.RestrictedRelations())
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
	// Completed definitions join the current statement's private catalog, never
	// the committed root. Finish alone publishes the complete IMPORT and effects.
	if scope.Catalog == nil {
		scope.Catalog = scope.PendingCatalog().Clone()
	}
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
		if err := executeImportedDefinition(e, scope, table, definition.Name, stage.Base.Epoch()); err != nil {
			return core.Result{}, err
		}
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

// Each definition consumes a fresh stage over the newest private predecessor.
// The initial IMPORT stage separately retains discovery dependencies, including
// when the provider returns no selected definitions.
func executeImportedDefinition(e *core.Engine, scope *core.StatementScope, table *parser.ForeignDDL, name string, epoch ids.CatalogEpoch) error {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err == nil && e.Catalog.State.Epoch() != epoch {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.Mu.Unlock()
		return fdw.ImportError(err, name)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	validations, _, err := fdw.StageTable(tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, table, scope.Input)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return fdw.ImportError(err, name)
	}
	return fdw.ImportError(finishForeignTableCreation(e, scope, stage, validations), name)
}
