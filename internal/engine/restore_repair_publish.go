package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// PublishRestoreRepairLocked is the shared tail of the Restore catalog
// repairs that replace one relation: next, decided under Engine.mu against
// auth's catalog, goes through a private stage and publishes with no
// statement scope. The caller holds e.mu; this releases it before
// validation and publication, and discards the stage on every exit.
func PublishRestoreRepairLocked(e *core.Engine, op *session.Operation, auth core.AuthorizationView, next *catalog.Relation, epoch ids.CatalogEpoch) error {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: epoch})
	return err
}
