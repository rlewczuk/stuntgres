package session

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

// SerializableWriteSurvives reports whether t still holds the private write
// that event recorded on address: a rewrite of the relation, the deletion of
// the event's base version of its row, or the written version. Rollback keeps
// the serializable identity evidence of surviving writes only. The caller
// holds Engine.mu and t.Mu.
func (t *TransactionOwner) SerializableWriteSurvives(address ids.ObjectAddress, event visibility.SerializableWrite) bool {
	if event.Rewrite {
		return t.Catalog != nil && t.Catalog.Storage[address].Rewritten
	}
	if event.Deletion {
		root := t.Deletes[address]
		if root == nil {
			return false
		}
		candidate, ok := root.Rows[event.Row]
		return ok && candidate.Version == event.Base
	}
	for _, row := range t.Writes[address] {
		if row.Version == event.Version {
			return true
		}
	}
	return false
}
