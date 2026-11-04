package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// SepgsqlColumnRetirement is called by the genuine column DROP effect owner,
// after ordinary dependency/RESTRICT admission and before retiring its labels.
// Capturing an immutable preparation stores no MAC grants or client subject.
func (p *DropProvider) SepgsqlColumnRetirement(previous, next *catalog.Relation) error {
	if p.Stage == nil || p.InternalRemoval || p.Owner.Sepgsql == nil || p.Stage.terminalCleanup {
		return nil
	}
	for _, attribute := range previous.Attributes {
		if next.HasAttribute(attribute) {
			continue
		}
		address := previous.Address()
		address.SubID = attribute
		if consumed, err := p.consumeSepgsqlRetirement(address); err != nil {
			return err
		} else if consumed {
			continue
		}
		sources := map[ids.ObjectAddress]catalog.Descriptor{address: previous}
		if err := p.prepareSepgsqlRetirements([]ids.ObjectAddress{address}, nil, sources); err != nil {
			return err
		}
		if _, err := p.consumeSepgsqlRetirement(address); err != nil {
			return err
		}
	}
	return nil
}
