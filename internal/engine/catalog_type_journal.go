package engine

// The domain and enum slots of the catalog journal. A nil slot is an empty
// one. Journals copied by value share slots, so a slot is never written in
// place: domainWritable and enumWritable first replace it with a private
// copy (the maps and slices inside keep their old sharing rules).

func (c *catalogChanges) domainWritable() *DomainJournal {
	next := new(DomainJournal)
	if c.domain != nil {
		*next = *c.domain
	}
	c.domain = next
	return next
}

func (c *catalogChanges) enumWritable() *EnumJournal {
	next := new(EnumJournal)
	if c.enum != nil {
		*next = *c.enum
	}
	c.enum = next
	return next
}
