package catalog

import "stuntgres/internal/engine/ids"

// ParameterIncarnation fences replacement even when the sparse authority is
// empty before and after Reset/Restore. Ordinary ACL updates do not advance it.
func (s *State) ParameterIncarnation() uint64 { return s.parameterIncarnation }

// SetParameterIncarnation mutates only an unpublished root.
func (s *State) SetParameterIncarnation(value uint64) { s.parameterIncarnation = value }

// ParametersUnchanged compares only shared parameter replacement authority.
// Unrelated local descriptors and other shared classes may legitimately change
// while Restore reads input; their existing retention/validation rules still
// apply. Both roots remain immutable, including their canonical name bindings.
func (s *State) ParametersUnchanged(predecessor *State) bool {
	if s.ParameterIncarnation() != predecessor.ParameterIncarnation() {
		return false
	}
	if s.descriptors == predecessor.descriptors && s.names == predecessor.names {
		return true
	}
	return predecessor.descriptors.Every(func(address ids.ObjectAddress, descriptor Descriptor) bool {
		return address.ClassID != ids.ParameterClassID ||
			s.Get(address) == descriptor && s.NameAddress(DescriptorNameKey(descriptor)) == address
	}) && s.descriptors.Every(func(address ids.ObjectAddress, descriptor Descriptor) bool {
		return address.ClassID != ids.ParameterClassID ||
			predecessor.Get(address) == descriptor && predecessor.NameAddress(DescriptorNameKey(descriptor)) == address
	})
}

// InheritParametersLocked installs current shared authority at final database
// registration, not from the possibly older template seed.
func (s *Store) InheritParametersLocked(next *State) {
	for address, descriptor := range next.Descriptors() {
		if address.ClassID == ids.ParameterClassID {
			next.UnbindName(DescriptorNameKey(descriptor))
			next.RemoveDescriptor(address)
		}
	}
	for address, descriptor := range s.State.Descriptors() {
		if address.ClassID == ids.ParameterClassID {
			next.SetDescriptor(address, descriptor)
			next.BindName(DescriptorNameKey(descriptor), address)
		}
	}
	next.SetParameterIncarnation(s.State.ParameterIncarnation())
}

// WritesParameters includes removals, including a private first grant that was
// later removed. Such work must never cross a native authority replacement.
func (c *Candidate) WritesParameters() bool {
	for address := range c.Descriptors() {
		if address.ClassID == ids.ParameterClassID { return true }
	}
	for address := range c.Removals() {
		if address.ClassID == ids.ParameterClassID { return true }
	}
	return false
}

func (c *Candidate) ParameterPreflight(current *State) error {
	if c.WritesParameters() && c.Base.State().ParameterIncarnation() != current.ParameterIncarnation() {
		return ids.ErrCatalogReplaced
	}
	return nil
}
