package engine

import (
	"stuntgres/internal/engine/ids"
)

type indexBuildWaitKind uint8

const (
	indexWaitWriters indexBuildWaitKind = iota + 1
	indexWaitOlderSnapshots
)

type indexBuildWait struct {
	owner ids.TxRef
	kind  indexBuildWaitKind
}

// indexWaitRegistry holds the CONCURRENTLY build waits that InspectIndex
// reports, by index. It has no mutex: Engine.mu guards it, and every method
// requires the caller to hold Engine.mu.
type indexWaitRegistry struct {
	waits map[ids.ObjectAddress]indexBuildWait
}

// admitLocked records that owner's build of index waits for kind.
func (r *indexWaitRegistry) admitLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	if r.waits == nil {
		r.waits = make(map[ids.ObjectAddress]indexBuildWait)
	}
	r.waits[index] = indexBuildWait{owner: owner, kind: kind}
}

// clearLocked drops the wait of index if it is still owner's wait for kind.
func (r *indexWaitRegistry) clearLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	if r.waits[index] == (indexBuildWait{owner: owner, kind: kind}) {
		delete(r.waits, index)
	}
}

// endOwnerLocked drops every wait owner holds; its transaction ended.
func (r *indexWaitRegistry) endOwnerLocked(owner ids.TxRef) {
	for index, wait := range r.waits {
		if wait.owner == owner {
			delete(r.waits, index)
		}
	}
}

// clearAllLocked drops every wait; the serializable fence replaced the epoch.
func (r *indexWaitRegistry) clearAllLocked() { clear(r.waits) }

// lookupLocked returns the wait recorded for index.
func (r *indexWaitRegistry) lookupLocked(index ids.ObjectAddress) (indexBuildWait, bool) {
	wait, ok := r.waits[index]
	return wait, ok
}
