package ddl

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Check checks req with auth as both the view and its environment, as the
// engine's authorization view does.
func Check(h Host, auth Authority, req access.Requirement) error {
	return h.AccessView(auth).Check(auth, req)
}

// ObjectRef is the provenance-stamped reference to a in auth's catalog.
func ObjectRef(h Host, auth Authority, a ids.ObjectAddress) ids.ObjectRef {
	view := h.AccessView(auth)
	return view.Owner.Domain.Ref(view.Catalog.Epoch(), a)
}

// Notice emits a NOTICE, as schema DDL does.
func Notice(h Host, code, message string) {
	h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: code, Message: message})
}
