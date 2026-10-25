package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// enrollSerializableLocked fences ref against the current catalog epoch and
// applies its isolation to the tracker. Caller holds Engine.mu.
func (e *Engine) enrollSerializableLocked(ref ids.TxRef, isolation session.Isolation) {
	if ref.ID() == 0 || ref.Epoch() != e.Catalog.State.Epoch() {
		return
	}
	e.Visibility.EnrollSerializableLocked(ref, isolation == session.Serializable)
}

func (e *Engine) endSerializable(ref ids.TxRef, committed bool) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	e.endSerializableLocked(ref, committed)
}

// endSerializableLocked ends ref in the registry and drops its index build
// waits. Caller holds Engine.mu.
func (e *Engine) endSerializableLocked(ref ids.TxRef, committed bool) {
	e.Visibility.EndTransactionLocked(ref, committed)
	e.IndexWaits.EndOwnerLocked(ref)
}

// Epoch replacement wakes every unresolved acquisition. Operation/ref epoch
// validation supplies the ordinary fence error, not a serialization diagnostic.
func (e *Engine) fenceSerializableLocked() {
	e.Visibility.FenceLocked()
	e.IndexWaits.ClearAllLocked()
}
