package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
)

// initializeQueryLocksLocked validates the query's row-lock legality over the
// statement's private catalog (view.QueryLocks) and records its demand: the
// visibility snapshot demand, and a query lock context when rows are locked.
// Called before any source expression is opened.
func (e *Engine) initializeQueryLocksLocked(scope *StatementScope, root plan.Node, leaves []exec.Leaf) error {
	demand, err := view.QueryLocks(e.privateCatalogLocked(scope.owner.Catalog), root, leaves)
	scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || demand.Visibility
	if err != nil {
		return err
	}
	if demand.Marked {
		if scope.queryLocks == nil {
			scope.queryLocks = &exec.LockContext{}
		} else {
			// Imported policy roots extend the same physical occurrence
			// manifest; never discard earlier leaves or a cursor's identities.
			scope.queryLocks.IdentityOnly = false
		}
	}
	return nil
}
