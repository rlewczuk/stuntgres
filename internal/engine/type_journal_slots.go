package engine

import (
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// domainJournal holds the committed-predecessor fences of ALTER DOMAIN.
type DomainJournal struct {
	Validations []*DomainValidationFence
	IndexRoots  map[ids.ObjectAddress]*storage.IndexRoot
}

// enumJournal holds enum provenance: types created at top transaction level
// and globally nonreused member identities.
type EnumJournal struct {
	Created map[ids.ObjectAddress]bool
	Unsafe  map[int64]bool
}

// clone copies the slot for a savepoint or statement candidate.
func (d *DomainJournal) Clone() *DomainJournal {
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

// clone copies the slot for a savepoint or statement candidate.
func (j *EnumJournal) Clone() *EnumJournal {
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
