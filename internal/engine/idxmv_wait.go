package engine

import (
	"stuntgres/internal/engine/ids"
)

type indexBuildWaitKind uint8

const (
	WaitWriters indexBuildWaitKind = iota + 1
	WaitOlderSnapshots
)

type indexBuildWait struct {
	Owner ids.TxRef
	Kind  indexBuildWaitKind
}

// indexWaitRegistry holds the CONCURRENTLY build waits that InspectIndex
// reports, by index. It has no mutex: Engine.mu guards it, and every method
// requires the caller to hold Engine.mu.
type indexWaitRegistry struct {
	Waits map[ids.ObjectAddress]indexBuildWait
}

// admitLocked records that owner's build of index waits for kind.
func (r *indexWaitRegistry) AdmitLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	if r.Waits == nil {
		r.Waits = make(map[ids.ObjectAddress]indexBuildWait)
	}
	r.Waits[index] = indexBuildWait{Owner: owner, Kind: kind}
}

// clearLocked drops the wait of index if it is still owner's wait for kind.
func (r *indexWaitRegistry) ClearLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	if r.Waits[index] == (indexBuildWait{Owner: owner, Kind: kind}) {
		delete(r.Waits, index)
	}
}

// endOwnerLocked drops every wait owner holds; its transaction ended.
func (r *indexWaitRegistry) EndOwnerLocked(owner ids.TxRef) {
	for index, wait := range r.Waits {
		if wait.Owner == owner {
			delete(r.Waits, index)
		}
	}
}

// clearAllLocked drops every wait; the serializable fence replaced the epoch.
func (r *indexWaitRegistry) ClearAllLocked() { clear(r.Waits) }

// lookupLocked returns the wait recorded for index.
func (r *indexWaitRegistry) LookupLocked(index ids.ObjectAddress) (indexBuildWait, bool) {
	wait, ok := r.Waits[index]
	return wait, ok
}
