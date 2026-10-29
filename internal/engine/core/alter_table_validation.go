package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// TransferAlterStage consumes genuine, structurally checked private metadata
// and physical stages. The registered providers are demanded once by the final
// fresh validation stage, after every relation's physical work has completed.
func (e *Engine) TransferAlterStage(scope *StatementScope, stage *CatalogStage) error {
	defer scope.releaseCatalogReservation()
	scope.releaseCatalogReservation()
	if err := e.normalizeCatalogStage(scope.Op.Context(), stage); err != nil {
		return err
	}
	read, err := e.catalogStageValidationRead(stage)
	if err != nil {
		return err
	}
	for _, d := range stage.Descriptors() {
		var provider catalog.Provider
		switch d.Address().ClassID {
		case ids.RelationClassID:
			provider = &RelationProvider
		case ids.TypeClassID:
			provider = stored.TypeProvider{}
		case ids.StatisticExtClassID:
			provider = catalog.StatisticsProvider{}
		case ids.ProcedureClassID:
			// Type-generation normalization also reowns installed SQL recipes.
			provider = stored.ProcedureProvider{}
		case ids.PublicationRelationClassID:
			provider = catalog.PublicationRelationProvider{}
		case ids.PublicationNamespaceClassID:
			provider = catalog.PublicationNamespaceProvider{}
		default:
			return fmt.Errorf("stuntgres: unexpected ALTER metadata class %d", d.Address().ClassID)
		}
		if err := e.validateCatalogDescriptor(stage, read, d, provider); err != nil {
			return err
		}
	}
	if err := scope.acquireCatalogStageInputs(stage); err != nil {
		return err
	}
	// Prospective destination names remain protected by their original journal
	// observations, not held claims across reentrant providers/callbacks.
	if err := scope.deferAlterCatalogNames(stage); err != nil {
		return err
	}
	if err := scope.AcquireRoutineDefinitionProviders(stage); err != nil {
		return err
	}
	scope.releaseCatalogReservation()
	return e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone())
}

func (phase AlterMetadataPhase) Record(stage *CatalogStage) {
	for address, descriptor := range stage.Descriptors() {
		phase.final[address] = descriptor
	}
	for address := range stage.Removals() {
		phase.final[address] = nil
	}
}

// FinishAlterValidation validates the exact final descriptors without another
// replacement, revision, or resurrection. Prior genuine transfers retain their
// original committed catalog, graph, index and storage publication fences.
func (e *Engine) FinishAlterValidation(scope *StatementScope, phase AlterMetadataPhase) error {
	defer scope.releaseCatalogReservation()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	for address, descriptor := range phase.final {
		if auth.Catalog.Get(address) != descriptor {
			e.Mu.Unlock()
			e.DiscardCatalogStage(stage)
			return catalog.Conflict{}
		}
		if descriptor != nil {
			stage.alterFinal = append(stage.alterFinal, descriptor)
			stage.definitionSources = append(stage.definitionSources, descriptor)
		}
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	scope.releaseCatalogReservation()
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return err
}
