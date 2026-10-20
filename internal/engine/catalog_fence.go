package engine

// catalogFence is one feature's commit precondition in the transaction
// journal. A fence is immutable once recorded; journal clones and savepoint
// marks share it. Each feature declares its fence type next to its code.
type catalogFence interface {
	fenceKind() catalogFenceKind
	// currentLocked reports whether the fence still holds. Caller holds
	// Engine.mu.
	currentLocked(e *Engine, c *catalogChanges) error
}

// catalogFenceKind fixes where catalogPreflightLocked checks a fence. The
// kinds run at the positions and in the order listed there, so the first
// error a commit reports does not depend on how fences are stored.
type catalogFenceKind uint8

const (
	fenceStorageRead        catalogFenceKind = iota // partitionReadFence
	fenceIndexSet                                   // catalogIndexFence
	fenceNamespace                                  // catalogNamespaceFence
	fenceExtensionAuthority                         // extensionAuthorityFence
	fenceTruncateGraph                              // truncateGraphFence
	fenceMaterializedSource                         // materializedSourceFence
)

func (c *catalogChanges) addFence(f catalogFence) {
	c.fences = append(c.fences, f)
}

// catalogFencesCurrentLocked checks the fences of each kind in turn, each kind
// in recording order. Caller holds Engine.mu.
func (e *Engine) catalogFencesCurrentLocked(c *catalogChanges, kinds ...catalogFenceKind) error {
	for _, kind := range kinds {
		for _, f := range c.fences {
			if f.fenceKind() != kind {
				continue
			}
			if err := f.currentLocked(e, c); err != nil {
				return err
			}
		}
	}
	return nil
}
