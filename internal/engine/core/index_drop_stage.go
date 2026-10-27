package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

var _ index.DropStage = (*CatalogStage)(nil)

// StageRead reads the stage over its base.
func (s *CatalogStage) StageRead() catalog.Read { return s.Owner.StageRead(s) }

// ReplaceRelationProjection retains entering identities before the checked
// descriptor replacement. Records remain private until overlay admission.
// The caller holds nothing; replacement remains stage-local.
func (s *CatalogStage) ReplaceRelationProjection(before, after *catalog.Relation) error {
	p := StageDropProvider(nil, s, nil, nil, nil)
	records, err := p.CaptureRelationProjection(before, after)
	if err != nil {
		return err
	}
	if err := index.StagePartitionDescriptor(s, after); err != nil {
		return err
	}
	s.DropRecords = append(s.DropRecords, records...)
	return nil
}

// RemoveRelation stages the removal of the relation at address. Like Remove,
// it takes Engine.mu; the caller holds nothing.
func (s *CatalogStage) RemoveRelation(address ids.ObjectAddress) error {
	return s.Remove(s.Owner.Catalog.Domain.Ref(s.Base.Epoch(), address))
}

var _ index.ValidityTarget = (*DropProvider)(nil)
