package session

import "stuntgres/internal/engine/ids"

// descriptorEdit is one immutable descriptor write in a journal's history.
// Serials are diagnostic positions only; ancestry is decided by node identity.
type descriptorEdit struct {
	address ids.ObjectAddress
	serial  uint64
	prev    *descriptorEdit
}

// DescriptorEditMark is an opaque comparable position in one journal's
// immutable descriptor-edit history. The zero mark denotes the empty history.
type DescriptorEditMark struct{ edit *descriptorEdit }

// DescriptorEditMark returns the journal's current history position.
func (c *Journal) DescriptorEditMark() DescriptorEditMark {
	if c == nil {
		return DescriptorEditMark{}
	}
	return DescriptorEditMark{edit: c.descriptorEdits}
}

// DescriptorEditSerial returns the current tail serial, or zero for an empty
// history. Serials are diagnostic and never authenticate ancestry.
func (c *Journal) DescriptorEditSerial() uint64 {
	if c == nil || c.descriptorEdits == nil {
		return 0
	}
	return c.descriptorEdits.serial
}

// DescriptorEditsSince visits, newest first, the descriptor write addresses
// recorded after mark, calling visit once per write (duplicates are possible).
// It allocates no slice and stops early when visit returns false. It reports
// false when mark is not an ancestor of this journal's chain or when the
// descriptor map and the edit history disagree on the number of distinct
// addresses; callers must then treat the history as untrusted.
func (c *Journal) DescriptorEditsSince(mark DescriptorEditMark, visit func(ids.ObjectAddress) bool) bool {
	if c == nil || len(c.Descriptors) != c.descriptorEditKeys {
		return false
	}
	if mark.edit != nil {
		ancestor := false
		for node := c.descriptorEdits; node != nil; node = node.prev {
			if node == mark.edit {
				ancestor = true
				break
			}
		}
		if !ancestor {
			return false
		}
	}
	for node := c.descriptorEdits; node != nil && node != mark.edit; node = node.prev {
		if !visit(node.address) {
			return true
		}
	}
	return true
}
