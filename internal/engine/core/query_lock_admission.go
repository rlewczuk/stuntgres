package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
)

// initializeQueryLocksLocked validates the query's row-lock legality over the
// statement's private catalog (view.QueryLocks) and records its demand: the
// visibility snapshot demand, and a physical manifest for locks or identity reads.
// Called before any source expression is opened.
func (e *Engine) initializeQueryLocksLocked(scope *StatementScope, root plan.Node, leaves []exec.Leaf) error {
	demand, err := view.QueryLocks(e.PrivateCatalogLocked(scope.PendingCatalog()), root, leaves)
	scope.Inquiry.visibilityDemand = scope.Inquiry.visibilityDemand || demand.Visibility
	scope.exportRootDemand = scope.exportRootDemand || demand.ExportRoots
	if err != nil {
		return err
	}
	if demand.Marked {
		if scope.QueryLockContext == nil {
			scope.QueryLockContext = &exec.LockContext{}
		} else {
			// Imported policy roots extend the same physical occurrence
			// manifest; never discard earlier leaves or a cursor's identities.
			scope.QueryLockContext.IdentityOnly = false
		}
	} else if scope.QueryLockContext == nil && demand.IdentityReads {
		// Materialized hierarchy/view adapters need their physical occurrence
		// manifest even when tableoid or a virtual recipe is the only consumer.
		scope.QueryLockContext = &exec.LockContext{IdentityOnly: true}
	}
	return nil
}
