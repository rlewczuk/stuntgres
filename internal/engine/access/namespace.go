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
