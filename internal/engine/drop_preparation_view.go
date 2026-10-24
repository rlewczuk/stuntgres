package engine

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// dropPreparationView reads Base through a prepared drop's effects, its
// Prior chain and its definition input. A nil Preparation reads Base alone;
// IdentitySource is the grant-time identity sequence provider, if any.
type dropPreparationView struct {
	Base           catalog.Read
	Preparation    *preparedDrop
	IdentitySource *catalog.Relation
}

type indexLayoutReader interface {
	IndexLayout(ids.ObjectAddress) (index.LayoutLineage, bool)
}

// Replacement is the descriptor that replaces address, or nil.
func (v dropPreparationView) Replacement(address ids.ObjectAddress) catalog.Descriptor {
	for prepared := v.Preparation; prepared != nil; prepared = prepared.Prior {
		if slot := prepared.slots[address]; slot.descriptor != 0 {
			effect := prepared.effects[slot.descriptor-1]
			if effect.Remove.Address == address {
				return nil
			}
			if effect.After != nil {
				return effect.After
			}
			return effect.Descriptor
		}
	}
	if v.IdentitySource != nil && v.IdentitySource.Address() == address {
		return v.IdentitySource
	}
	if v.Preparation != nil && v.Preparation.Input != nil {
		descriptor, present := v.Preparation.Input.Descriptor(address)
		if present && descriptor != v.Preparation.Base.Get(address) {
			return descriptor
		}
	}
	return nil
}

// Removed reports whether the preparation chain or its input removed address.
func (v dropPreparationView) Removed(address ids.ObjectAddress) bool {
	for prepared := v.Preparation; prepared != nil; prepared = prepared.Prior {
		if prepared.slots[address].removed {
			return true
		}
	}
	if v.Preparation != nil && v.Preparation.Input != nil && v.Preparation.Base.Get(address) != nil {
		_, present := v.Preparation.Input.Descriptor(address)
		return !present
	}
	return false
}

// Descriptors enumerates Base as the input projects it, then the input's
// additions.
func (v dropPreparationView) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return v.DescriptorsOver(func() catalog.Read { return v.Base })
}

// DescriptorsOver is Descriptors over the base that base returns at each read,
// for a drop whose base an admission replaces while a caller iterates.
func (v dropPreparationView) DescriptorsOver(base func() catalog.Read) iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		input := v.input()
		for address, descriptor := range base().Descriptors() {
			if input != nil {
				if projected, present := input.Descriptor(address); present {
					descriptor = projected
				}
			}
			if !yield(address, descriptor) {
				return
			}
		}
		if input == nil {
			return
		}
		for address, descriptor := range input.Descriptors() {
			if base().Get(address) == nil && !yield(address, descriptor) {
				return
			}
		}
	}
}

// DescriptorCount counts what Descriptors enumerates.
func (v dropPreparationView) DescriptorCount() int {
	count := v.Base.DescriptorCount()
	if input := v.input(); input != nil {
		for address := range input.Descriptors() {
			if v.Base.Get(address) == nil {
				count++
			}
		}
	}
	return count
}

// IndexLayout is the latest layout the chain recorded for address, else the
// input's.
func (v dropPreparationView) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	for prepared := v.Preparation; prepared != nil; prepared = prepared.Prior {
		if slot := prepared.slots[address]; slot.layout != 0 {
			return *prepared.effects[slot.layout-1].Layout, true
		}
	}
	if v.Preparation != nil {
		if input, present := v.Preparation.Input.(indexLayoutReader); present {
			return input.IndexLayout(address)
		}
	}
	return index.LayoutLineage{}, false
}

// InputDescriptor reads the actual metadata entering the preparation. It never
// substitutes that candidate for the captured predecessor used by Observe.
func (v dropPreparationView) InputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if v.IdentitySource != nil && v.IdentitySource.Address() == address {
		return v.IdentitySource
	}
	if v.Preparation != nil && v.Preparation.Input != nil {
		descriptor, present := v.Preparation.Input.Descriptor(address)
		if present || v.Preparation.Base.Get(address) != nil {
			return descriptor
		}
	}
	return v.Base.Get(address)
}

func (v dropPreparationView) input() catalog.DefinitionReader {
	if v.Preparation == nil {
		return nil
	}
	return v.Preparation.Input
}
