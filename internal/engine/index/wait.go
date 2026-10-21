package index

import (
	"stuntgres/internal/engine/ids"
)

type BuildWaitKind uint8

const (
	WaitWriters BuildWaitKind = iota + 1
	WaitOlderSnapshots
)

type BuildWait struct {
	Owner ids.TxRef
	Kind  BuildWaitKind
}

// WaitRegistry holds the CONCURRENTLY build waits that InspectIndex
// reports, by index. It has no mutex: Engine.mu guards it, and every method
// requires the caller to hold Engine.mu.
type WaitRegistry struct {
	Waits map[ids.ObjectAddress]BuildWait
}

// AdmitLocked records that owner's build of index waits for kind.
func (r *WaitRegistry) AdmitLocked(index ids.ObjectAddress, owner ids.TxRef, kind BuildWaitKind) {
	if r.Waits == nil {
		r.Waits = make(map[ids.ObjectAddress]BuildWait)
	}
	r.Waits[index] = BuildWait{Owner: owner, Kind: kind}
}

// ClearLocked drops the wait of index if it is still owner's wait for kind.
func (r *WaitRegistry) ClearLocked(index ids.ObjectAddress, owner ids.TxRef, kind BuildWaitKind) {
	if r.Waits[index] == (BuildWait{Owner: owner, Kind: kind}) {
		delete(r.Waits, index)
	}
}

// EndOwnerLocked drops every wait owner holds; its transaction ended.
func (r *WaitRegistry) EndOwnerLocked(owner ids.TxRef) {
	for index, wait := range r.Waits {
		if wait.Owner == owner {
			delete(r.Waits, index)
		}
	}
}

// ClearAllLocked drops every wait; the serializable fence replaced the epoch.
func (r *WaitRegistry) ClearAllLocked() { clear(r.Waits) }

// LookupLocked returns the wait recorded for index.
func (r *WaitRegistry) LookupLocked(index ids.ObjectAddress) (BuildWait, bool) {
	wait, ok := r.Waits[index]
	return wait, ok
}
