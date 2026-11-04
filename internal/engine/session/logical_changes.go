package session

import "stuntgres/internal/engine/logical"

// MergeLogicalChanges returns t's captured changes followed by changes. It may
// append into the spare capacity of t's slice; savepoint marks keep clipped
// copies.
func (t *TransactionOwner) MergeLogicalChanges(changes []logical.Change) []logical.Change {
	if len(t.LogicalChanges) == 0 {
		return changes
	}
	return append(t.LogicalChanges, changes...)
}
