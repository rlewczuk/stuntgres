package access

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// CheckPersistentCreationNamespace keeps the TEMP capability boundary of
// non-table creation and namespace moves.
func CheckPersistentCreationNamespace(view catalog.Read, namespace ids.ObjectAddress) error {
	if view.MustNamespace(namespace).Temporary != (ids.SessionToken{}) {
		return sqlError("0A000", "temporary schema creation is unsupported")
	}
	return nil
}

// CheckTemporaryStorageAccess rejects another session's stored TEMP relation.
// Namespace lookup and role privileges precede physical storage admission.
// Identity/metadata providers do not acquire another session's stored TEMP data.
func CheckTemporaryStorageAccess(view catalog.Read, relation *catalog.Relation, token ids.SessionToken) error {
	if relation == nil {
		return ids.ErrCatalogReplaced
	}
	if !relation.RelationKind.Stored() {
		return nil
	}
	namespace, ok := view.Namespace(relation.Namespace())
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != token {
		return sqlError("0A000", "cannot access temporary tables of other sessions")
	}
	return nil
}
