package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// CheckSetNamespace is shared by internal namespace movers. It does not grant
// ownership or CREATE; those gates belong to each actual utility entry.
func CheckSetNamespace(view Read, source, destination ids.ObjectAddress) error {
	from, fromOK := view.Namespace(source)
	to, toOK := view.Namespace(destination)
	if !fromOK || !toOK {
		return ids.ErrCatalogReplaced
	}
	message := ""
	if from.Temporary != (ids.SessionToken{}) || to.Temporary != (ids.SessionToken{}) {
		message = "cannot move objects into or out of temporary schemas"
	} else if source.ObjectID == ids.PGToastNamespaceOID || destination.ObjectID == ids.PGToastNamespaceOID {
		message = "cannot move objects into or out of TOAST schema"
	}
	if message == "" {
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: message}
}
