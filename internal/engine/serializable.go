package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// enrollSerializableLocked fences ref against the current catalog epoch and
// applies its isolation to the tracker. Caller holds Engine.mu.
func (e *Engine) enrollSerializableLocked(ref ids.TxRef, isolation session.Isolation) {
	if ref.ID() == 0 || ref.Epoch() != e.catalog.State.Epoch() {
		return
	}
	e.visibility.EnrollSerializableLocked(ref, isolation == session.Serializable)
}

func (e *Engine) endSerializable(ref ids.TxRef, committed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.endSerializableLocked(ref, committed)
}

// endSerializableLocked ends ref in the registry and drops its index build
// waits. Caller holds Engine.mu.
func (e *Engine) endSerializableLocked(ref ids.TxRef, committed bool) {
	e.visibility.EndTransactionLocked(ref, committed)
	e.indexWaits.EndOwnerLocked(ref)
}

// Epoch replacement wakes every unresolved acquisition. Operation/ref epoch
// validation supplies the ordinary fence error, not a serialization diagnostic.
func (e *Engine) fenceSerializableLocked() {
	e.visibility.FenceLocked()
	e.indexWaits.ClearAllLocked()
}
