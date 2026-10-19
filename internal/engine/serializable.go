package engine

import (
	"stuntgres/internal/engine/ids"
)

func stableIsolation(level Isolation) bool { return level == RepeatableRead || level == Serializable }

// enrollSerializableLocked fences ref against the current catalog epoch and
// applies its isolation to the tracker. Caller holds Engine.mu.
func (e *Engine) enrollSerializableLocked(ref ids.TxRef, isolation Isolation) {
	if ref.ID() == 0 || ref.Epoch() != e.catalog.state.Epoch() {
		return
	}
	e.visibility.EnrollSerializableLocked(ref, isolation == Serializable)
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
	for index, wait := range e.indexBuildWaits {
		if wait.owner == ref {
			delete(e.indexBuildWaits, index)
		}
	}
}

// Epoch replacement wakes every unresolved acquisition. Operation/ref epoch
// validation supplies the ordinary fence error, not a serialization diagnostic.
func (e *Engine) fenceSerializableLocked() {
	e.visibility.FenceLocked()
	clear(e.indexBuildWaits)
}
