package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

var _ index.DropStage = (*CatalogStage)(nil)

// StageRead reads the stage over its base.
func (s *CatalogStage) StageRead() catalog.Read { return s.Owner.StageRead(s) }

// RemoveRelation stages the removal of the relation at address. Like Remove,
// it takes Engine.mu; the caller holds nothing.
func (s *CatalogStage) RemoveRelation(address ids.ObjectAddress) error {
	return s.Remove(s.Owner.Catalog.Domain.Ref(s.Base.Epoch(), address))
}

var _ index.ValidityTarget = (*DropProvider)(nil)
