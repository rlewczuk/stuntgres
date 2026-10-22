package typedef

import (
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

var (
	_ session.DomainSlot = (*DomainJournal)(nil)
	_ session.EnumSlot   = (*EnumJournal)(nil)
)

func (*DomainJournal) DomainSlot() {}
func (*EnumJournal) EnumSlot()     {}

// DomainJournal holds the committed-predecessor fences of ALTER DOMAIN.
type DomainJournal struct {
	Validations []*DomainValidationFence
	IndexRoots  map[ids.ObjectAddress]*storage.IndexRoot
}

// EnumJournal holds enum provenance: types created at top transaction level
// and globally nonreused member identities.
type EnumJournal struct {
	Created map[ids.ObjectAddress]bool
	Unsafe  map[int64]bool
}

// Clone copies the slot for a savepoint or statement candidate. A nil
// journal clones to a nil slot.
func (d *DomainJournal) Clone() session.DomainSlot {
	if d == nil {
		return nil
	}
	n := &DomainJournal{Validations: append([]*DomainValidationFence(nil), d.Validations...)}
	if d.IndexRoots != nil {
		n.IndexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(d.IndexRoots))
		maps.Copy(n.IndexRoots, d.IndexRoots)
	}
	return n
}

func (d *DomainJournal) ValidationFences() []*DomainValidationFence {
	if d == nil {
		return nil
	}
	return d.Validations
}

func (d *DomainJournal) CommittedIndexRoots() map[ids.ObjectAddress]*storage.IndexRoot {
	if d == nil {
		return nil
	}
	return d.IndexRoots
}

// Clone copies the slot for a savepoint or statement candidate. A nil
// journal clones to a nil slot.
func (j *EnumJournal) Clone() session.EnumSlot {
	if j == nil {
		return nil
	}
	n := new(EnumJournal)
	if j.Created != nil {
		n.Created = make(map[ids.ObjectAddress]bool, len(j.Created))
		maps.Copy(n.Created, j.Created)
	}
	if j.Unsafe != nil {
		n.Unsafe = make(map[int64]bool, len(j.Unsafe))
		maps.Copy(n.Unsafe, j.Unsafe)
	}
	return n
}

func (j *EnumJournal) CreatedTypes() map[ids.ObjectAddress]bool {
	if j == nil {
		return nil
	}
	return j.Created
}

func (j *EnumJournal) UnsafeMembers() map[int64]bool {
	if j == nil {
		return nil
	}
	return j.Unsafe
}

// The domain and enum slots of the catalog journal. A nil slot is an empty
// one. Journals copied by value share slots, so a slot is never written in
// place: DomainWritable and EnumWritable first replace it with a private
// copy (the maps and slices inside keep their old sharing rules).

// DomainWritable gives c a private domain slot and returns it.
func DomainWritable(c *session.Journal) *DomainJournal {
	next := new(DomainJournal)
	if c.Domain != nil {
		*next = *DomainJournalOf(c.Domain)
	}
	c.Domain = domainSlotOf(next)
	return next
}

// EnumWritable gives c a private enum slot and returns it.
func EnumWritable(c *session.Journal) *EnumJournal {
	next := new(EnumJournal)
	if c.Enum != nil {
		*next = *EnumJournalOf(c.Enum)
	}
	c.Enum = EnumSlotOf(next)
	return next
}

// EnumJournalOf returns the enum journal in s, or nil.
func EnumJournalOf(s session.EnumSlot) *EnumJournal {
	j, _ := s.(*EnumJournal)
	return j
}

// EnumSlotOf stores j; a nil journal stays a nil slot.
func EnumSlotOf(j *EnumJournal) session.EnumSlot {
	if j == nil {
		return nil
	}
	return j
}

// DomainJournalOf returns the domain journal in s, or nil.
func DomainJournalOf(s session.DomainSlot) *DomainJournal {
	d, _ := s.(*DomainJournal)
	return d
}

// domainSlotOf stores d; a nil journal stays a nil slot.
func domainSlotOf(d *DomainJournal) session.DomainSlot {
	if d == nil {
		return nil
	}
	return d
}
