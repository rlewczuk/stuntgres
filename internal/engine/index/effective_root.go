package index

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// EffectiveRoot returns the index root a transaction sees for address: its
// private root from journal when it has one, else the committed root. A nil
// journal reads the committed root. The caller holds Engine.mu over committed.
func EffectiveRoot(committed map[ids.ObjectAddress]*storage.IndexRoot, journal *session.Journal, address ids.ObjectAddress) *storage.IndexRoot {
	if journal != nil {
		if root := JournalIndexesOrZero(journal).UniqueRoots[address]; root != nil {
			return root
		}
	}
	return committed[address]
}
