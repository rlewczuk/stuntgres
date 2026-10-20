package engine

import (
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// The domain and enum slots of the catalog journal. A nil slot is an empty
// one. Journals copied by value share slots, so a slot is never written in
// place: domainWritable and enumWritable first replace it with a private
// copy (the maps and slices inside keep their old sharing rules).

// domainJournal holds the committed-predecessor fences of ALTER DOMAIN.
type domainJournal struct {
	validations []*domainValidationFence
	indexRoots  map[ids.ObjectAddress]*storage.IndexRoot
}

// enumJournal holds enum provenance: types created at top transaction level
// and globally nonreused member identities.
type enumJournal struct {
	created map[ids.ObjectAddress]bool
	unsafe  map[int64]bool
}

func (c *catalogChanges) domainWritable() *domainJournal {
	next := new(domainJournal)
	if c.domain != nil {
		*next = *c.domain
	}
	c.domain = next
	return next
}

func (c *catalogChanges) enumWritable() *enumJournal {
	next := new(enumJournal)
	if c.enum != nil {
		*next = *c.enum
	}
	c.enum = next
	return next
}

// clone copies the slot for a savepoint or statement candidate.
func (d *domainJournal) clone() *domainJournal {
	if d == nil {
		return nil
	}
	n := &domainJournal{validations: append([]*domainValidationFence(nil), d.validations...)}
	if d.indexRoots != nil {
		n.indexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(d.indexRoots))
		maps.Copy(n.indexRoots, d.indexRoots)
	}
	return n
}

func (d *domainJournal) validationFences() []*domainValidationFence {
	if d == nil {
		return nil
	}
	return d.validations
}

func (d *domainJournal) committedIndexRoots() map[ids.ObjectAddress]*storage.IndexRoot {
	if d == nil {
		return nil
	}
	return d.indexRoots
}

// clone copies the slot for a savepoint or statement candidate.
func (j *enumJournal) clone() *enumJournal {
	if j == nil {
		return nil
	}
	n := new(enumJournal)
	if j.created != nil {
		n.created = make(map[ids.ObjectAddress]bool, len(j.created))
		maps.Copy(n.created, j.created)
	}
	if j.unsafe != nil {
		n.unsafe = make(map[int64]bool, len(j.unsafe))
		maps.Copy(n.unsafe, j.unsafe)
	}
	return n
}

func (j *enumJournal) createdTypes() map[ids.ObjectAddress]bool {
	if j == nil {
		return nil
	}
	return j.created
}

func (j *enumJournal) unsafeMembers() map[int64]bool {
	if j == nil {
		return nil
	}
	return j.unsafe
}
