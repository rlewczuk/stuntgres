package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
)

// Only the already-admitted exact first-child publication can advance this
// predecessor. Every other base entry/name/epoch remains the original snapshot;
// this is receipt consumption, never adoption of the current catalog.
func (stage *CatalogStage) ConsumeInheritanceMarkerReceipt(receipt *InheritanceRelationshipAdmission) error {
	previous, ok := stage.Base.Relation(receipt.Ref.Address)
	if !receipt.Admitted || !ok || !table.InheritanceMarkerPublication(previous, receipt.Relation) {
		return catalog.Conflict{}
	}
	base := catalog.CloneState(stage.Base.State())
	base.SetDescriptor(receipt.Ref.Address, receipt.Relation)
	stage.Base = stage.Base.WithState(base)
	return stage.Replace(receipt.Relation)
}
