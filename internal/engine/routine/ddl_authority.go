package routine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
)

// ddlCheck checks req with auth as both the view and its environment, as the
// engine's authorization view does.
func ddlCheck(h DDLHost, auth Authority, req access.Requirement) error {
	return h.AccessView(auth).Check(auth, req)
}

// ddlRef is the provenance-stamped reference to a in auth's catalog.
func ddlRef(h DDLHost, auth Authority, a ids.ObjectAddress) ids.ObjectRef {
	view := h.AccessView(auth)
	return view.Owner.Domain.Ref(view.Catalog.Epoch(), a)
}
