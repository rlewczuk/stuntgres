package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
)

// The domain and enum slots of the catalog journal. A nil slot is an empty
// one. Journals copied by value share slots, so a slot is never written in
// place: domainWritable and enumWritable first replace it with a private
// copy (the maps and slices inside keep their old sharing rules).

func domainWritable(c *session.Journal) *typedef.DomainJournal {
	next := new(typedef.DomainJournal)
	if c.Domain != nil {
		*next = *domainJournalOf(c.Domain)
	}
	c.Domain = domainSlotOf(next)
	return next
}

func enumWritable(c *session.Journal) *typedef.EnumJournal {
	next := new(typedef.EnumJournal)
	if c.Enum != nil {
		*next = *enumJournalOf(c.Enum)
	}
	c.Enum = enumSlotOf(next)
	return next
}

// enumJournalOf returns the enum journal in s, or nil.
func enumJournalOf(s session.EnumSlot) *typedef.EnumJournal {
	j, _ := s.(*typedef.EnumJournal)
	return j
}

// enumSlotOf stores j; a nil journal stays a nil slot.
func enumSlotOf(j *typedef.EnumJournal) session.EnumSlot {
	if j == nil {
		return nil
	}
	return j
}

// domainJournalOf returns the domain journal in s, or nil.
func domainJournalOf(s session.DomainSlot) *typedef.DomainJournal {
	d, _ := s.(*typedef.DomainJournal)
	return d
}

// domainSlotOf stores d; a nil journal stays a nil slot.
func domainSlotOf(d *typedef.DomainJournal) session.DomainSlot {
	if d == nil {
		return nil
	}
	return d
}
