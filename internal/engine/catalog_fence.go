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

// The root fence kinds follow the session kinds. catalogPreflightLocked checks
// every kind at a fixed position and in a fixed order, so the first error a
// commit reports does not depend on how fences are stored.
const (
	fenceExtensionAuthority = session.FenceRootKinds + iota // extensionAuthorityFence
	fenceTruncateGraph                                      // truncateGraphFence
	fenceMaterializedSource                                 // materializedSourceFence
	fenceCatalogRoleRead                                    // catalogRoleReadFence
)

// catalogFencesCurrentLocked checks the fences of each kind in turn, each kind
// in recording order. Caller holds Engine.mu.
func (e *Engine) catalogFencesCurrentLocked(c *session.Journal, kinds ...session.FenceKind) error {
	for _, kind := range kinds {
		for _, f := range c.Fences {
			if f.Kind() != kind {
				continue
			}
			var err error
			switch fence := f.(type) {
			case session.CommittedFence:
				err = fence.Current(e.catalog.state, e.storage.Relations)
			case catalogFence:
				err = fence.currentLocked(e, c)
			default:
				return fmt.Errorf("stuntgres: foreign catalog fence %T", f)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
