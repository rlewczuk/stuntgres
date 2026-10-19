package visibility

import (
	"stuntgres/internal/engine/ids"
)

// PredicateLockHolder is a relevant serializable transaction and the
// relations it has read (SIRead locks). reads is the tracker's live set: read
// it only while Engine.mu is still held.
type PredicateLockHolder struct {
	Ref      ids.TxRef
	Prepared bool
	Reads    map[ids.ObjectAddress]struct{}
}

// PredicateLocksLocked lists the relevant serializable transactions of epoch
// with their read sets. Caller holds Engine.mu.
func (reg *Registry) PredicateLocksLocked(epoch ids.CatalogEpoch) []PredicateLockHolder {
	var holders []PredicateLockHolder
	for ref, node := range reg.Serializable.Nodes {
		if ref.Epoch() != epoch || node.Ref != ref || !serializableRelevant(node) {
			continue
		}
		holders = append(holders, PredicateLockHolder{Ref: ref, Prepared: node.Prepared, Reads: node.Reads})
	}
	return holders
}
