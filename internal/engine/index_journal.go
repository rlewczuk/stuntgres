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
	uniqueRoots map[ids.ObjectAddress]*storage.IndexRoot
	candidates  map[ids.ObjectAddress]indexRootCandidate // Private produced DDL roots and their exact baseline pins.
	rebuilds    map[ids.ObjectAddress]*catalog.Relation
}

// journalIndexes returns c's index slot, allocating it on first use.
func journalIndexes(c *session.Journal) *indexJournal {
	if c.Indexes == nil {
		c.Indexes = indexSlotOf(new(indexJournal))
	}
	return indexJournalOf(c.Indexes)
}

// journalIndexesOrZero reads c's index slot; a nil slot reads as zero.
func journalIndexesOrZero(c *session.Journal) indexJournal {
	if j := indexJournalOf(c.Indexes); j != nil {
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
	if j.uniqueRoots != nil {
		n.uniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(j.uniqueRoots))
		for a, root := range j.uniqueRoots {
			n.uniqueRoots[a] = root
		}
	}
	if j.candidates != nil {
		n.candidates = make(map[ids.ObjectAddress]indexRootCandidate, len(j.candidates))
		for a, candidate := range j.candidates {
			n.candidates[a] = candidate
		}
	}
	if j.rebuilds != nil {
		n.rebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(j.rebuilds))
		for a, d := range j.rebuilds {
			n.rebuilds[a] = d
		}
	}
	return n
}

// forget drops the private unique root and produced candidate of address.
func (j *indexJournal) forget(address ids.ObjectAddress) {
	if j == nil {
		return
	}
	delete(j.uniqueRoots, address)
	delete(j.candidates, address)
}

// pending reports whether the slot holds index work to publish.
func (j *indexJournal) pending() bool {
	return j != nil && (len(j.uniqueRoots) != 0 || len(j.candidates) != 0 || len(j.rebuilds) != 0)
}

var _ session.IndexSlot = (*indexJournal)(nil)

func (*indexJournal) IndexSlot() {}

// Clone implements session.IndexSlot.
func (j *indexJournal) Clone() session.IndexSlot { return indexSlotOf(j.clone()) }

// indexJournalOf returns the index journal in s, or nil.
func indexJournalOf(s session.IndexSlot) *indexJournal {
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
