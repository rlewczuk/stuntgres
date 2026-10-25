package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
)

// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func (e *Engine) RefreshIdentitySequenceLocked(scope *StatementScope, stage *CatalogStage, table *catalog.Relation, attribute int32, address ids.ObjectAddress) error {
	fresh, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	if fresh.Catalog.Epoch() != stage.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	descriptor := fresh.Catalog.Get(address)
	if staged, exists := stage.Find(address); exists {
		descriptor = staged
	}
	if _, err := sequence.IdentityDefinition(descriptor, table, attribute); err != nil {
		return err
	}
	stage.SequenceState().CaptureIdentityOriginal(address, e.Catalog.State.Get(address))
	stage.Base = fresh.Catalog
	return nil
}
