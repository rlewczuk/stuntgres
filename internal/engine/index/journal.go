package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Journal is the transaction journal's index state: private unique
// roots, produced index candidates and REINDEX intents. Index DDL and TEMP
// table creation write it; DML only reads it.
type Journal struct {
	UniqueRoots map[ids.ObjectAddress]*storage.IndexRoot
	Candidates  map[ids.ObjectAddress]RootCandidate // Private produced DDL roots and their exact baseline pins.
	Rebuilds    map[ids.ObjectAddress]*catalog.Relation
}

// JournalIndexes returns c's index slot, allocating it on first use.
func JournalIndexes(c *session.Journal) *Journal {
	if c.Indexes == nil {
		c.Indexes = indexSlotOf(new(Journal))
	}
	return JournalOf(c.Indexes)
}

// JournalIndexesOrZero reads c's index slot; a nil slot reads as zero.
func JournalIndexesOrZero(c *session.Journal) Journal {
	if j := JournalOf(c.Indexes); j != nil {
		return *j
	}
	return Journal{}
}

// clone copies the slot for a new journal. The journal is copy-on-write and
// savepoint marks keep older journals, so the maps are copied, keeping nil
// maps nil.
func (j *Journal) clone() *Journal {
	if j == nil {
		return nil
	}
	n := &Journal{}
	if j.UniqueRoots != nil {
		n.UniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(j.UniqueRoots))
		for a, root := range j.UniqueRoots {
			n.UniqueRoots[a] = root
		}
	}
	if j.Candidates != nil {
		n.Candidates = make(map[ids.ObjectAddress]RootCandidate, len(j.Candidates))
		for a, candidate := range j.Candidates {
			n.Candidates[a] = candidate
		}
	}
	if j.Rebuilds != nil {
		n.Rebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(j.Rebuilds))
		for a, d := range j.Rebuilds {
			n.Rebuilds[a] = d
		}
	}
	return n
}

// Forget drops the private unique root and produced candidate of address.
func (j *Journal) Forget(address ids.ObjectAddress) {
	if j == nil {
		return
	}
	delete(j.UniqueRoots, address)
	delete(j.Candidates, address)
}

// Pending reports whether the slot holds index work to publish.
func (j *Journal) Pending() bool {
	return j != nil && (len(j.UniqueRoots) != 0 || len(j.Candidates) != 0 || len(j.Rebuilds) != 0)
}

var _ session.IndexSlot = (*Journal)(nil)

func (*Journal) IndexSlot() {}

// Clone implements session.IndexSlot.
func (j *Journal) Clone() session.IndexSlot { return indexSlotOf(j.clone()) }

// JournalOf returns the index journal in s, or nil.
func JournalOf(s session.IndexSlot) *Journal {
	j, _ := s.(*Journal)
	return j
}

// indexSlotOf stores j; a nil journal stays a nil slot.
func indexSlotOf(j *Journal) session.IndexSlot {
	if j == nil {
		return nil
	}
	return j
}
