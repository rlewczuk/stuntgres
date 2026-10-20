package engine

import (
	"fmt"

	"stuntgres/internal/engine/session"
)

// catalogFence is one feature's commit precondition in the transaction
// journal, stored there as a session.Fence. A fence is immutable once
// recorded; journal clones and savepoint marks share it. Each feature declares
// its fence type next to its code.
type catalogFence interface {
	session.Fence
	// currentLocked reports whether the fence still holds. Caller holds
	// Engine.mu.
	currentLocked(e *Engine, c *session.Journal) error
}

// The fence kinds fix where catalogPreflightLocked checks a fence. The kinds
// run at the positions and in the order listed there, so the first error a
// commit reports does not depend on how fences are stored.
const (
	fenceStorageRead        session.FenceKind = iota // partitionReadFence
	fenceIndexSet                                    // catalogIndexFence
	fenceNamespace                                   // catalogNamespaceFence
	fenceExtensionAuthority                          // extensionAuthorityFence
	fenceTruncateGraph                               // truncateGraphFence
	fenceMaterializedSource                          // materializedSourceFence
)

// catalogFencesCurrentLocked checks the fences of each kind in turn, each kind
// in recording order. Caller holds Engine.mu.
func (e *Engine) catalogFencesCurrentLocked(c *session.Journal, kinds ...session.FenceKind) error {
	for _, kind := range kinds {
		for _, f := range c.Fences {
			if f.Kind() != kind {
				continue
			}
			fence, ok := f.(catalogFence)
			if !ok {
				return fmt.Errorf("stuntgres: foreign catalog fence %T", f)
			}
			if err := fence.currentLocked(e, c); err != nil {
				return err
			}
		}
	}
	return nil
}
