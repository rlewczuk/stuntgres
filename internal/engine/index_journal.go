package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// indexJournal is the transaction journal's index state: private unique
// roots, produced index candidates and REINDEX intents. Index DDL and TEMP
// table creation write it; DML only reads it.
type indexJournal struct {
	UniqueRoots map[ids.ObjectAddress]*storage.IndexRoot
	Candidates  map[ids.ObjectAddress]indexRootCandidate // Private produced DDL roots and their exact baseline pins.
	Rebuilds    map[ids.ObjectAddress]*catalog.Relation
}

// journalIndexes returns c's index slot, allocating it on first use.
func JournalIndexes(c *session.Journal) *indexJournal {
	if c.Indexes == nil {
		c.Indexes = indexSlotOf(new(indexJournal))
	}
	return JournalOf(c.Indexes)
}

// journalIndexesOrZero reads c's index slot; a nil slot reads as zero.
func JournalIndexesOrZero(c *session.Journal) indexJournal {
	if j := JournalOf(c.Indexes); j != nil {
		return *j
	}
	return indexJournal{}
}

// clone copies the slot for a new journal. The journal is copy-on-write and
// savepoint marks keep older journals, so the maps are copied, keeping nil
// maps nil.
func (j *indexJournal) clone() *indexJournal {
	if j == nil {
		return nil
	}
	n := &indexJournal{}
	if j.UniqueRoots != nil {
		n.UniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(j.UniqueRoots))
		for a, root := range j.UniqueRoots {
			n.UniqueRoots[a] = root
		}
	}
	if j.Candidates != nil {
		n.Candidates = make(map[ids.ObjectAddress]indexRootCandidate, len(j.Candidates))
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

// forget drops the private unique root and produced candidate of address.
func (j *indexJournal) Forget(address ids.ObjectAddress) {
	if j == nil {
		return
	}
	delete(j.UniqueRoots, address)
	delete(j.Candidates, address)
}

// pending reports whether the slot holds index work to publish.
func (j *indexJournal) Pending() bool {
	return j != nil && (len(j.UniqueRoots) != 0 || len(j.Candidates) != 0 || len(j.Rebuilds) != 0)
}

var _ session.IndexSlot = (*indexJournal)(nil)

func (*indexJournal) IndexSlot() {}

// Clone implements session.IndexSlot.
func (j *indexJournal) Clone() session.IndexSlot { return indexSlotOf(j.clone()) }

// indexJournalOf returns the index journal in s, or nil.
func JournalOf(s session.IndexSlot) *indexJournal {
	j, _ := s.(*indexJournal)
	return j
}

// indexSlotOf stores j; a nil journal stays a nil slot.
func indexSlotOf(j *indexJournal) session.IndexSlot {
	if j == nil {
		return nil
	}
	return j
}
