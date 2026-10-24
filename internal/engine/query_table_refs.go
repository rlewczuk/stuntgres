package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
)

// Expand recursive view bases before the first capture. Capture callers pass
// any already-required target refs, preserving a single statement frontier.
// The view and catalog owner checks stay here; view.QueryTableRefs walks.
func (e *Engine) queryTableRefsLocked(auth AuthorizationView, invoker authorization.PrincipalRef, leaves []exec.Leaf, refs []ids.ObjectRef, token ids.SessionToken, inherited plan.RowMark) ([]ids.ObjectRef, error) {
	read := auth.Catalog
	if auth.Owner != &e.catalog.Owner || read.Owner() != &e.catalog.Owner || read.State() == nil {
		return nil, ids.ErrCatalogReplaced
	}
	return view.QueryTableRefs(auth.View, auth, invoker, leaves, refs, token, inherited)
}
